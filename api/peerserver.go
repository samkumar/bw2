// This file is part of BOSSWAVE.
//
// BOSSWAVE is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// BOSSWAVE is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with BOSSWAVE.  If not, see <http://www.gnu.org/licenses/>.
//
// Copyright © 2015 Michael Andersen <m.andersen@cs.berkeley.edu>

package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/context"

	log "github.com/cihub/seelog"
	"github.com/immesys/bw2/crypto"
	"github.com/immesys/bw2/internal/core"
	"github.com/immesys/bw2/util/bwe"
)

func genCert(vk string) (tls.Certificate, *x509.Certificate) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Criticalf("failed to generate serial number: %s", err)
		panic(err)
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: vk,
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	template.IsCA = true
	template.KeyUsage |= x509.KeyUsageCertSign
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		log.Criticalf("Failed to create certificate: %s", err)
		panic(err)
	}
	x509cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		panic(err)
	}

	keybytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	certbytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	cert, err := tls.X509KeyPair(certbytes, keybytes)
	if err != nil {
		panic(err)
	}
	return cert, x509cert
}

func Start(bw *BW) {
	//Generate TLS certificate
	vk := crypto.FmtKey(bw.Entity.GetVK())
	cert, cert2 := genCert(vk)
	tlsConfig := tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := tls.Listen("tcp", bw.Config.Native.ListenOn, &tlsConfig)
	log.Info("peer server listening on:", bw.Config.Native.ListenOn)
	if err != nil {
		log.Criticalf("Could not open native adapter socket: %v", err)
		os.Exit(1)
	}
	proof := make([]byte, 32+64)
	copy(proof, bw.Entity.GetVK())
	if err != nil {
		log.Criticalf("Could not parse certificate")
		log.Flush()
		os.Exit(1)
	}
	crypto.SignBlob(bw.Entity.GetSK(), bw.Entity.GetVK(), proof[32:], cert2.Signature)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Criticalf("Socket error: %v", err)
		}
		//First thing we do is write the 96 byte proof that the self-signed cert was
		//generated by the person posessing the router's SK
		conn.Write(proof)
		//Create a client
		cl := bw.CreateClient(context.Background(), "PEER:"+conn.RemoteAddr().String())
		//Then handle the session
		go handleSession(cl, conn)
	}
}

type nativeFrame struct {
	length uint64
	seqno  uint64
	cmd    uint8
	body   []byte
}

const (
	nCmdMessage = 1

	nCmdEnd     = 5
	nCmdRStatus = 6
	nCmdRSub    = 7
	nCmdResult  = 8
)

func handleSession(cl *BosswaveClient, conn net.Conn) {
	log.Info("peer ", conn.RemoteAddr().String(), " connected on ", conn.LocalAddr().String())
	defer func() {
		cl.ctxCancel()
	}()
	hdr := make([]byte, 17)

	rmutex := sync.Mutex{}

	reply := func(f *nativeFrame) {
		//log.Infof("Sending reply of length %v to seqno %v", len(f.body), f.seqno)
		tmphdr := make([]byte, 17)
		binary.LittleEndian.PutUint64(tmphdr, uint64(len(f.body)))
		binary.LittleEndian.PutUint64(tmphdr[8:], f.seqno)
		tmphdr[16] = byte(f.cmd)
		rmutex.Lock()
		defer rmutex.Unlock()
		conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
		_, err := conn.Write(tmphdr)
		if err != nil {
			log.Info("peer write error: ", err.Error())
			conn.Close()
			cl.ctxCancel()
			return
		}
		_, err = conn.Write(f.body)
		if err != nil {
			log.Info("peer write error: ", err.Error())
			conn.Close()
			cl.ctxCancel()
		}
	}
	errframe := func(seqno uint64, code int, msg string) {
		rv := nativeFrame{
			seqno: seqno,
			cmd:   nCmdRStatus,
			body:  make([]byte, 2+len(msg)),
		}
		binary.LittleEndian.PutUint16(rv.body, uint16(code))
		copy(rv.body[2:], []byte(msg))
		reply(&rv)
	}

	for {
		_, err := io.ReadFull(conn, hdr)
		if err != nil {
			log.Info("peer error: ", err.Error())
			return
		}
		nf := nativeFrame{}
		nf.length = binary.LittleEndian.Uint64(hdr)
		nf.seqno = binary.LittleEndian.Uint64(hdr[8:])
		nf.cmd = hdr[16]
		nf.body = make([]byte, nf.length)
		_, err = io.ReadFull(conn, nf.body)
		if err != nil {
			log.Info("peer error: ", err.Error())
			return
		}

		go func() {
			switch nf.cmd {
			case nCmdMessage:
				msg, err := core.LoadMessage(nf.body)
				//log.Info("Load message returned")
				if err != nil {
					log.Info("Load message error: ", err.Error())
					errframe(nf.seqno, bwe.MalformedMessage, err.Error())
					return
				}
				err = cl.VerifyAffinity(msg)
				if err != nil {
					errframe(nf.seqno, bwe.AffinityMismatch, err.Error())
					return
				}
				err = msg.Verify(cl.BW())
				if err != nil {
					bws := bwe.AsBW(err)
					errframe(nf.seqno, bws.Code, bws.Msg)
					log.Infof("message failed verification: %#v", msg)
					if msg.PrimaryAccessChain != nil {
						log.Infof("pac src %v\n", crypto.FmtKey(msg.PrimaryAccessChain.GetGiverVK()))
						log.Infof("pac dst %v\n", crypto.FmtKey(msg.PrimaryAccessChain.GetReceiverVK()))
					}
					log.Infof("roz are %#v\n", msg.RoutingObjects)
					if msg.OriginVK != nil {
						log.Infof("msg src %\v\n", crypto.FmtKey(*msg.OriginVK))
					} else {
						log.Infof("msg has no origin VK header\n")
					}
					return
				}
				//log.Info("message verified ok")

				switch msg.Type {
				case core.TypePublish:
					errframe(nf.seqno, bwe.Okay, "")
					cl.cl.Publish(msg)
				case core.TypePersist:
					errframe(nf.seqno, bwe.Okay, "")
					cl.cl.Persist(msg)
				case core.TypeUnsubscribe:
					err := cl.cl.Unsubscribe(msg.UnsubUMid)
					if err == nil {
						errframe(nf.seqno, bwe.Okay, "")
					} else {
						errframe(nf.seqno, bwe.UnsubscribeError, "Unsubscribe error: "+err.Error())
					}

				case core.TypeSubscribe, core.TypeTap:
					subid := cl.cl.Subscribe(cl.ctx, msg, func(m *core.Message) {
						if m == nil {
							rv := nativeFrame{
								seqno: nf.seqno,
								cmd:   nCmdEnd,
							}
							reply(&rv)
						} else {
							rv := nativeFrame{
								seqno: nf.seqno,
								cmd:   nCmdResult,
								body:  m.Encoded,
							}
							reply(&rv)
						}
					})
					rv := nativeFrame{
						seqno: nf.seqno,
						cmd:   nCmdRSub,
						body:  make([]byte, 18),
					}
					binary.LittleEndian.PutUint16(rv.body, uint16(bwe.Okay))
					binary.LittleEndian.PutUint64(rv.body[2:], subid.Mid)
					binary.LittleEndian.PutUint64(rv.body[10:], subid.Sig)
					reply(&rv)
				case core.TypeQuery, core.TypeTapQuery:
					errframe(nf.seqno, bwe.Okay, "")
					cl.cl.Query(msg, func(m *core.Message) {
						rv := nativeFrame{
							seqno: nf.seqno,
						}
						if m == nil {
							rv.cmd = nCmdEnd
							rv.body = []byte{}
						} else {
							rv.cmd = nCmdResult
							rv.body = m.Encoded
						}
						reply(&rv)
					})
				case core.TypeLS:
					errframe(nf.seqno, bwe.Okay, "")
					cl.cl.List(msg, func(uri string, ok bool) {
						rv := nativeFrame{
							seqno: nf.seqno,
						}
						if !ok {
							rv.cmd = nCmdEnd
							rv.body = []byte{}
						} else {
							rv.cmd = nCmdResult
							rv.body = []byte(uri)
						}
						reply(&rv)
					})
				default:
					errframe(nf.seqno, bwe.BadOperation, "type mismatch")
					return
				}
			default: //nCmd
				errframe(nf.seqno, bwe.BadOperation, "what command is this?")
				return
			}
		}()
	}
}

package crypto

// #cgo LDFLAGS: -lssl -lcrypto
// #include "ed25519.h"
import "C"

import (
	"encoding/base64"
	"unsafe"
)

//SignVector will generate a signature on the arguments, in order
//and return it
func SignVector(sk []byte, vk []byte, into []byte, vec ...[]byte) {
	if len(into) != 64 {
		panic("Into must be exactly 64 bytes long")
	}
	lens := make([]C.size_t, len(vec))
	for i, v := range vec {
		lens[i] = C.size_t(len(v))
	}
	//From SO user jimt
	var b *C.char
	ptrSize := unsafe.Sizeof(b)

	// Allocate the char** list.
	ptr := C.malloc(C.size_t(len(vec)) * C.size_t(ptrSize))
	defer C.free(ptr)

	// Assign each byte slice to its appropriate offset.
	for i := 0; i < len(vec); i++ {
		element := (**C.char)(unsafe.Pointer(uintptr(ptr) + uintptr(i)*ptrSize))
		*element = (*C.char)(unsafe.Pointer(&vec[i][0]))
	}

	C.ed25519_sign_vector((**C.uchar)(ptr),
		(*C.size_t)(unsafe.Pointer(&lens[0])),
		(C.size_t)(len(vec)),
		(*C.uchar)(unsafe.Pointer(&sk[0])),
		(*C.uchar)(unsafe.Pointer(&vk[0])),
		(*C.uchar)(unsafe.Pointer(&into[0])))
}

func SignBlob(sk []byte, vk []byte, into []byte, blob []byte) {
	if len(into) != 64 {
		panic("into must be exactly 64 bytes long")
	}
	C.ed25519_sign((*C.uchar)(unsafe.Pointer(&blob[0])),
		(C.size_t)(len(blob)),
		(*C.uchar)(unsafe.Pointer(&sk[0])),
		(*C.uchar)(unsafe.Pointer(&vk[0])),
		(*C.uchar)(unsafe.Pointer(&into[0])))
}

//VerifyBlob returns true if the sig is ok, false otherwise
func VerifyBlob(vk []byte, sig []byte, blob []byte) bool {
	rv := C.ed25519_sign_open((*C.uchar)(unsafe.Pointer(&blob[0])),
		(C.size_t)(len(blob)),
		(*C.uchar)(unsafe.Pointer(&vk[0])),
		(*C.uchar)(unsafe.Pointer(&sig[0])))
	return rv == 0
}

func GenerateKeypair() (sk []byte, vk []byte) {
	sk = make([]byte, 32)
	vk = make([]byte, 32)
	C.bw_generate_keypair((*C.uchar)(unsafe.Pointer(&sk[0])),
		(*C.uchar)(unsafe.Pointer(&vk[0])))
	return
}

func FmtKey(key []byte) string {
	return base64.URLEncoding.EncodeToString(key)
}

func FmtSig(sig []byte) string {
	return base64.URLEncoding.EncodeToString(sig)
}

func FmtHash(hash []byte) string {
	return base64.URLEncoding.EncodeToString(hash)
}

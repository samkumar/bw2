package bc

import (
	"context"
	"math/big"

	"github.com/immesys/bw2/util/bwe"
	"github.com/immesys/bw2bc/common"
	"github.com/immesys/bw2bc/common/math"
)

//TODO rewrite this to use UFI

//Builtin contract interfaces
const (
	// AliasAddress        = "0x04a640aeb0c0af5cad4ea8705de3608ad036106c"
	// AliasSigResolve     = "ea992c5d"
	// AliasSigCreateShort = "bf75fdb5"
	// AliasSigSet         = "111e73ff"
	AliasCreateShortCost = 100000
	AliasCreateLongCost  = 5000000

	// UFIs for Alias
	UFI_Alias_Address = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418"
	// DB(uint256 ) -> bytes32
	UFI_Alias_DB = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418018b51ab1040000000000000"
	// SetAlias(uint256 k, bytes32 v) ->
	UFI_Alias_SetAlias = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418111e73ff1400000000000000"
	// LastShort() -> uint256
	UFI_Alias_LastShort = "cc74681c3e3b7bcccf7a05524b75ba8feccc741811e026a50100000000000000"
	// ShortAliasPrice() -> uint256
	UFI_Alias_ShortAliasPrice = "cc74681c3e3b7bcccf7a05524b75ba8feccc74183135e00b0100000000000000"
	// AliasMin() -> uint256
	UFI_Alias_AliasMin = "cc74681c3e3b7bcccf7a05524b75ba8feccc74188bb523ae0100000000000000"
	// LongAliasPrice() -> uint256
	UFI_Alias_LongAliasPrice = "cc74681c3e3b7bcccf7a05524b75ba8feccc74189171a8190100000000000000"
	// CreateShortAlias(bytes32 v) ->
	UFI_Alias_CreateShortAlias = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418bf75fdb54000000000000000"
	// AliasFor(bytes32 ) -> uint256
	UFI_Alias_AliasFor = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418c83560ea4010000000000000"
	// Admin() -> address
	UFI_Alias_Admin = "cc74681c3e3b7bcccf7a05524b75ba8feccc7418ff1b636d0?00000000000000"
	// EVENT  AliasCreated(uint256 key, bytes32 value)
	EventSig_Alias_AliasCreated = "170b239b7d2c41f8c5caacdafe7409cda0f4b5012440739feea0576a40a156eb"
)

func (bc *blockChain) ResolveShortAlias(ctx context.Context, alias uint64) (res Bytes32, iszero bool, err error) {
	key := big.NewInt(int64(alias))
	keyarr := SliceToBytes32(math.PaddedBigBytes(key, 32))
	res, iszero, err = bc.ResolveAlias(ctx, keyarr)
	return
}

func (bc *blockChain) ResolveAlias(ctx context.Context, key Bytes32) (res Bytes32, iszero bool, err error) {
	// First check what the registry thinks of the DOTHash
	rvz, err := bc.CallOffChain(ctx, StringToUFI(UFI_Alias_DB), new(big.Int).SetBytes(key[:]))
	if err != nil || len(rvz) != 1 {
		return Bytes32{}, true, bwe.WrapM(bwe.UFIInvocationError, "Expected 1 rv: ", err)
	}

	res = SliceToBytes32(rvz[0].([]byte))
	iszer := res == Bytes32{}
	return res, iszer, nil
}

//CreateShortAlias creates an alias, waits (Confirmations) then locates the
//created short ID and sends it to the callback. If it times out (10 blocks)
//then and error is passed
func (bcc *bcClient) CreateShortAlias(ctx context.Context, acc int, val Bytes32, confirmed func(alias uint64, err error)) {
	if val.Zero() {
		confirmed(0, bwe.M(bwe.AliasError, "You cannot create an alias to zero"))
		return
	}

	gprice, err := bcc.bc.GasPrice(ctx)
	if err != nil {
		confirmed(0, err)
		return
	}
	sgprice := gprice.Text(10)
	cash := big.NewInt(AliasCreateShortCost)
	cash = cash.Mul(cash, gprice)
	txhash, err := bcc.CallOnChain(ctx, acc, StringToUFI(UFI_Alias_CreateShortAlias), cash.Text(10), "", sgprice, val)
	if err != nil {
		confirmed(0, err)
		return
	}

	bcc.bc.GetTransactionDetailsInt(ctx, txhash, bcc.DefaultTimeout, bcc.DefaultConfirmations,
		nil, func(bnum uint64, err error) {
			if err != nil {
				confirmed(0, err)
				return
			}
			rcpt := bcc.bc.GetTransactionReceipt(txhash)
			for _, lg := range rcpt.Logs {
				if lg.Topics[2] == common.Hash(val) {
					short := new(big.Int).SetBytes(lg.Topics[1][:]).Int64()
					confirmed(uint64(short), nil)
					return
				}
			}
			confirmed(0, bwe.M(bwe.AliasError, "Contract did not create alias"))
		})
}

func (bcc *bcClient) SetAlias(ctx context.Context, acc int, key Bytes32, val Bytes32, confirmed func(err error)) {
	if val.Zero() {
		confirmed(bwe.M(bwe.AliasError, "You cannot create an alias to zero"))
		return
	}
	rval, zero, err := bcc.bc.ResolveAlias(ctx, key)
	if err != nil {
		confirmed(bwe.WrapM(bwe.AliasError, "Preresolve error: ", err))
		return
	}
	if !zero {
		if rval == val {
			confirmed(bwe.M(bwe.AliasExists, "Alias exists (with the same value)"))
		} else {
			confirmed(bwe.M(bwe.AliasExists, "Alias exists (with a different value)"))
		}
		return
	}
	gprice, err := bcc.bc.GasPrice(ctx)
	if err != nil {
		confirmed(bwe.WrapM(bwe.AliasError, "Gas error: ", err))
		return
	}
	sgprice := gprice.Text(10)
	cash := big.NewInt(AliasCreateLongCost)
	cash = cash.Mul(cash, gprice)
	txhash, err := bcc.CallOnChain(ctx, acc, StringToUFI(UFI_Alias_SetAlias), cash.Text(10), "", sgprice, key, val)
	if err != nil {
		confirmed(err)
		return
	}

	bcc.bc.GetTransactionDetailsInt(ctx, txhash, bcc.DefaultTimeout, bcc.DefaultConfirmations,
		nil, func(bnum uint64, err error) {
			if err != nil {
				confirmed(err)
				return
			}
			v, _, err := bcc.bc.ResolveAlias(ctx, key)
			if err != nil {
				confirmed(err)
				return
			}
			if v != val {
				confirmed(bwe.M(bwe.AliasError, "Created alias contents do not match"))
				return
			}
			confirmed(nil)
			return
		})
}

func (bc *blockChain) UnresolveAlias(ctx context.Context, value Bytes32) (key Bytes32, iszero bool, err error) {
	ret, err := bc.CallOffChain(ctx, StringToUFI(UFI_Alias_AliasFor), value)
	if err != nil {
		return Bytes32{}, false, err
	}
	if len(ret) != 1 {
		return Bytes32{}, false, bwe.M(bwe.UFIInvocationError, "Expected 1 result")
	}
	k, ok := ret[0].(*big.Int)
	if !ok {
		return Bytes32{}, false, bwe.M(bwe.UFIInvocationError, "Expected bigint result")
	}
	key = Bytes32(common.BigToHash(k))
	return key, key == Bytes32{}, nil
}

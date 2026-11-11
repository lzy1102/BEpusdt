package model

import (
	"strings"
	"testing"
)

func TestWalletValidateXkoAddress(t *testing.T) {
	wa := Wallet{
		Address:   " XKO6b458ce9a7a29344c087697a87df155cf7128611 ",
		TradeType: string(UsdtXlayer),
	}

	if err := wa.Validate(); err != nil {
		t.Fatalf("XKO address should be accepted: %v", err)
	}

	const want = "0x6b458ce9a7a29344c087697a87df155cf7128611"
	if wa.Address != want {
		t.Fatalf("Address = %q, want %q", wa.Address, want)
	}
	if wa.MatchAddr != strings.ToLower(want) {
		t.Fatalf("MatchAddr = %q, want %q", wa.MatchAddr, strings.ToLower(want))
	}
}

func TestWalletValidateXkoRejectedOnOtherEvm(t *testing.T) {
	wa := Wallet{
		Address:   "XKO6b458ce9a7a29344c087697a87df155cf7128611",
		TradeType: string(UsdtErc20),
	}

	if err := wa.Validate(); err == nil {
		t.Fatal("XKO address must be rejected on non X Layer networks")
	}
}

package model

import "testing"

func TestOrderIsCurrencyAllowed(t *testing.T) {
	tests := []struct {
		name    string
		limit   string
		crypto  Crypto
		allowed bool
	}{
		{name: "empty allows all", limit: "", crypto: USDT, allowed: true},
		{name: "whitelist hit", limit: "USDT,USDC", crypto: USDT, allowed: true},
		{name: "whitelist miss", limit: "USDT,USDC", crypto: BNB, allowed: false},
		{name: "blacklist hit", limit: "-USDT", crypto: USDT, allowed: false},
		{name: "blacklist miss", limit: "-USDT", crypto: USDC, allowed: true},
		{name: "case insensitive", limit: "usdc", crypto: USDC, allowed: true},
		{name: "mixed", limit: "-USDT,USDC", crypto: USDC, allowed: true},
		{name: "mixed blacklist", limit: "-USDT,USDC", crypto: USDT, allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := Order{CurrencyLimit: tt.limit}
			if got := order.IsCurrencyAllowed(tt.crypto); got != tt.allowed {
				t.Fatalf("IsCurrencyAllowed(%s) = %v, want %v", tt.crypto, got, tt.allowed)
			}
		})
	}
}

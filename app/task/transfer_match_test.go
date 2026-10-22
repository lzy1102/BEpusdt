package task

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/v03413/bepusdt/app/model"
)

func TestGetReceivableOrdersExcludesConfirming(t *testing.T) {
	initTaskTestEnv(t, map[model.ConfKey]string{model.PaymentLookbackHour: "3"})

	now := time.Now()
	orders := []model.Order{
		{
			OrderId:   "match-waiting",
			TradeId:   "match-waiting",
			TradeType: model.UsdtTrc20,
			Fiat:      model.CNY,
			Crypto:    model.USDT,
			Rate:      "7.00",
			Amount:    "1.00",
			Money:     "7.00",
			Address:   "TWaitingAddress",
			Status:    model.OrderStatusWaiting,
			ExpiredAt: now.Add(10 * time.Minute),
		},
		{
			OrderId:   "match-expired",
			TradeId:   "match-expired",
			TradeType: model.UsdtTrc20,
			Fiat:      model.CNY,
			Crypto:    model.USDT,
			Rate:      "7.00",
			Amount:    "1.00",
			Money:     "7.00",
			Address:   "TExpiredAddress",
			Status:    model.OrderStatusExpired,
			ExpiredAt: now.Add(-5 * time.Minute),
		},
		{
			OrderId:   "match-confirming",
			TradeId:   "match-confirming",
			TradeType: model.UsdtTrc20,
			Fiat:      model.CNY,
			Crypto:    model.USDT,
			Rate:      "7.00",
			Amount:    "1.00",
			Money:     "7.00",
			Address:   "TConfirmingAddress",
			Status:    model.OrderStatusConfirming,
			ExpiredAt: now.Add(10 * time.Minute),
		},
	}

	for _, order := range orders {
		createTestOrder(t, order, now.Add(-time.Minute))
	}

	receivable := getReceivableOrders()
	for _, list := range receivable {
		for _, order := range list {
			if order.Status == model.OrderStatusConfirming {
				t.Fatalf("confirming order %s should not be receivable", order.TradeId)
			}
		}
	}

	if _, ok := receivable["TWaitingAddress"+string(model.UsdtTrc20)]; !ok {
		t.Fatal("waiting order should be receivable")
	}
	if _, ok := receivable["TExpiredAddress"+string(model.UsdtTrc20)]; !ok {
		t.Fatal("expired order within lookback window should be receivable")
	}
}

func TestExpiredOrderAllowsFinalLookback(t *testing.T) {
	initTaskTestEnv(t, nil)
	lookbackDone.Clear()

	now := time.Now()
	order := createTestOrder(t, model.Order{
		OrderId:   "expire-lookback",
		TradeId:   "expire-lookback",
		TradeType: model.UsdtTrc20,
		Fiat:      model.CNY,
		Crypto:    model.USDT,
		Rate:      "7.00",
		Amount:    "1.00",
		Money:     "7.00",
		Address:   "TExpireLookback",
		Status:    model.OrderStatusWaiting,
		ExpiredAt: now.Add(-time.Minute),
	}, now.Add(-2*time.Minute))

	lookbackDone.Store(order.ID, struct{}{})
	expireWaitingOrders()

	got := getTestOrder(t, order.TradeId)
	if got.Status != model.OrderStatusExpired {
		t.Fatalf("order status = %d, want expired", got.Status)
	}
	if _, ok := lookbackDone.Load(order.ID); ok {
		t.Fatal("expired order should be allowed to look back again")
	}
}

func TestMarkConfirmingOnlyOnce(t *testing.T) {
	initTaskTestEnv(t, nil)

	now := time.Now()
	order := createTestOrder(t, model.Order{
		OrderId:       "confirm-once",
		TradeId:       "confirm-once",
		TradeType:     model.UsdtTrc20,
		Fiat:          model.CNY,
		Crypto:        model.USDT,
		Rate:          "7.00",
		Amount:        "1.00",
		Money:         "7.00",
		Address:       "TConfirmOnce",
		AddressLocked: true,
		Status:        model.OrderStatusWaiting,
		ExpiredAt:     now.Add(10 * time.Minute),
	}, now.Add(-time.Minute))

	firstAt := now
	firstAmount := decimal.NewFromFloat(1.01)
	if err := order.MarkConfirming(1, "TFromOne", "hash-one", firstAt, firstAmount); err != nil {
		t.Fatalf("first MarkConfirming: %v", err)
	}

	got := getTestOrder(t, order.TradeId)
	if got.Status != model.OrderStatusConfirming || got.RefHash != "hash-one" || got.Amount != "1.01" || got.Money != "7.07" {
		t.Fatalf("unexpected order after first MarkConfirming: %+v", got)
	}

	err := order.MarkConfirming(2, "TFromTwo", "hash-two", firstAt.Add(time.Second), decimal.NewFromFloat(2.00))
	if err == nil {
		t.Fatal("second MarkConfirming should fail")
	}

	got = getTestOrder(t, order.TradeId)
	if got.RefHash != "hash-one" || got.Amount != "1.01" || got.Money != "7.07" {
		t.Fatalf("second MarkConfirming overwrote order: %+v", got)
	}
}

package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAmountJSON(t *testing.T) {
	tests := []struct {
		in   string
		want Amount
		out  string
	}{
		{"500", 50000, "500"},
		{"500.5", 50050, "500.5"},
		{"0.1", 10, "0.1"},
		{"729.98", 72998, "729.98"},
		{"0", 0, "0"},
	}
	for _, tt := range tests {
		var a Amount
		require.NoError(t, json.Unmarshal([]byte(tt.in), &a))
		assert.Equal(t, tt.want, a)
		b, err := json.Marshal(a)
		require.NoError(t, err)
		assert.Equal(t, tt.out, string(b))
	}

	var a Amount
	assert.Error(t, json.Unmarshal([]byte(`"abc"`), &a))
}

func TestAmountSumIsExact(t *testing.T) {
	var sum Amount
	for range 10 {
		sum += AmountFromFloat(0.1)
	}
	assert.Equal(t, AmountFromFloat(1), sum)
	assert.InDelta(t, 1.0, sum.Float(), 0)
}

func TestOrderStatusIsFinal(t *testing.T) {
	assert.False(t, StatusNew.IsFinal())
	assert.False(t, StatusProcessing.IsFinal())
	assert.True(t, StatusInvalid.IsFinal())
	assert.True(t, StatusProcessed.IsFinal())
}

func TestOrderJSON(t *testing.T) {
	ts := time.Date(2020, 12, 10, 15, 15, 45, 0, time.FixedZone("MSK", 3*3600))
	acc := Amount(50000)
	b, err := json.Marshal([]Order{
		{Number: "9278923470", Status: StatusProcessed, Accrual: &acc, UploadedAt: ts},
		{Number: "12345678903", Status: StatusProcessing, UploadedAt: ts},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"number":"9278923470","status":"PROCESSED","accrual":500,"uploaded_at":"2020-12-10T15:15:45+03:00"},
		{"number":"12345678903","status":"PROCESSING","uploaded_at":"2020-12-10T15:15:45+03:00"}
	]`, string(b))
}

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"sort"
	"time"

	"github.com/gofinance/ib"
)

// NewDefaultStkContract .
func NewDefaultStkContract(symbol string) ib.Contract {
	return ib.Contract{
		Symbol:       symbol,
		SecurityType: "STK",
		Currency:     "USD",
		Exchange:     "SMART",
	}
}

func subscribeTicker(eng *ib.Engine, contract ib.Contract) error {
	eventChan := make(chan ib.Reply)
	go func() {
		for event := range eventChan {
			tick, ok := event.(*ib.TickPrice)
			if !ok {
				continue
			}
			if tick.Type != ib.TickLast {
				continue
			}
			log.Printf("<-- %s: %#v\n", contract.Symbol, event)
		}
	}()

	nextID := eng.NextRequestID()
	eng.Subscribe(eventChan, nextID)

	req := &ib.RequestMarketData{
		Contract: contract,
	}
	req.SetID(nextID)
	if err := eng.Send(req); err != nil {
		return err
	}

	return nil
}

// Tick .
type Tick struct {
	Symbol   string    `json:"symbol"`   // Symbol identifier of the tick.
	Date     time.Time `json:"date"`     // Date of the tick.
	Span     string    `json:"size"`     // Span of the tick. // TODO: use int for seconds instead of string.
	Open     float64   `json:"open"`     // Open value of the tick.
	Close    float64   `json:"close"`    // Close value of the tick.
	High     float64   `json:"high"`     // High value of the tick.
	Low      float64   `json:"low"`      // Low value of the tick.
	Currency string    `json:"currency"` // Currency of the tick.
	Type     string    `json:"type"`     // Type of contract of the tick (STK, OPT, FUT, IND, FOP, CASH, BAG, NEWS).
}

// Ticks is a list of ticks.
type Ticks []Tick

// Len returns the length of the slice.
// Implements sort.Interface.
func (t Ticks) Len() int {
	return len(t)
}

// Less compares two ticks dates.
// Implements sort.Interface.
func (t Ticks) Less(i, j int) bool {
	return t[i].Date.After(t[j].Date)
}

// Swap swaps two ticks in the list.
// Implements sort.Interface.
func (t Ticks) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

// SimpleMovingAverage computes the SMA for the given span.
func (t Ticks) SimpleMovingAverage(offset, period int) float64 {
	if period == 0 {
		panic(fmt.Errorf("can't average on 0 elements"))
	}
	if (offset+1)-period < 0 {
		return 0.
	}
	var sum float64
	for i := (offset + 1) - period; i <= offset; i++ {
		sum += t[i].Close
	}
	return sum / float64(period)
}

// ExponentialMovingAverage computes the EMA.
func (t Ticks) ExponentialMovingAverage(offset, period int) float64 {
	multiplier := float64(2 / (float64(period) + 1))

	if len(t) < offset {
		panic(fmt.Errorf("EMA out of bounds"))
	}
	// If not enough points, return 0.
	if (offset+1)-period < 0 {
		return 0.
	} else if offset+1 == period { // If len == period, then return SMA.
		return t.SimpleMovingAverage(offset, 10)
	}
	return multiplier*(t[offset].Close-t.ExponentialMovingAverage(offset-1, period)) + t.ExponentialMovingAverage(offset-1, period)
}

func main() {
	tt := Ticks{
		{Close: 22.27},
		{Close: 22.19},
		{Close: 22.08},
		{Close: 22.17},
		{Close: 22.18},
		{Close: 22.13},
		{Close: 22.23},
		{Close: 22.43},
		{Close: 22.24},
		{Close: 22.29},
		{Close: 22.15},
		{Close: 22.39},
		{Close: 22.38},
		{Close: 22.61},
		{Close: 23.36},
		{Close: 24.05},
		{Close: 23.75},
	}
	for i, t := range tt {
		ema := tt.ExponentialMovingAverage(i, 10)
		sma := tt.SimpleMovingAverage(i, 10)
		fmt.Printf("%d\t%f\t%.2f\t%.4f\t%.2f\n", i+1, t.Close, sma, 0.1818, ema)
	}
}

func main3() {
	buf, err := ioutil.ReadFile("b.json")
	if err != nil {
		log.Fatalf("Error loading json file: %s\n", err)
	}
	ticks := Ticks{}
	if err := json.Unmarshal(buf, &ticks); err != nil {
		log.Fatalf("Error parsing json file: %s\n", err)
	}
	fmt.Printf("%d\n", len(ticks))

}

func main2() {
	opt := ib.EngineOptions{
		Gateway: "192.168.99.100:4003",
		//Gateway:          "localhost:4001",
		DumpConversation: true,
	}
	stateChan := make(chan ib.EngineState)
	eng, err := ib.NewEngine(opt)
	if err != nil {
		log.Fatal(err)
	}
	eng.SubscribeState(stateChan)

	go func() {
		for state := range stateChan {
			log.Printf("--> State change: %s\n", state.String())
		}
	}()
	time.Sleep(2 * time.Second)

	eventChan := make(chan ib.Reply)
	go func() {
		for event := range eventChan {
			log.Printf("<-- Event: %#v\n", event)
		}
	}()

	fct := func() {
		nextID := eng.NextRequestID()
		eng.Subscribe(eventChan, nextID)

		fmt.Printf("Subscribed\n")

		req := &ib.RequestAccountSummary{
			Group: "All",
			Tags:  "AccountType,NetLiquidation,TotalCashValue,BuyingPower",
		}
		req.SetID(nextID)
		if err := eng.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}
	}
	_ = fct

	//	subscribeTicker(eng, NewDefaultStkContract("TSLA"))

	tz, err := time.LoadLocation("America/New_York")
	if err != nil {
		log.Fatal(err)
	}

	var (
		symbol       = "AAPL"
		span         = ib.HistBarSize30Min
		currency     = "USD"
		contractType = "STK"
	)

	manager, err := ib.NewHistoricalDataManager(eng, ib.RequestHistoricalData{
		Contract:    NewDefaultStkContract(symbol),
		Duration:    "1 M",
		BarSize:     ib.HistDataBarSize(span),
		WhatToShow:  ib.HistMidpoint,
		UseRTH:      false,
		EndDateTime: time.Date(2016, time.June, 25, 20, 00, 00, 00, tz),
	})
	if err != nil {
		log.Fatal(err)
	}

	<-manager.Refresh()
	items := manager.Items()
	newItems := make(Ticks, 0, len(items))
	for _, item := range items {
		item.Date = item.Date.In(tz)
		newItems = append(newItems, Tick{
			Date:     item.Date.In(tz),
			Symbol:   symbol,
			Span:     span,
			Open:     item.Open,
			Close:    item.Close,
			High:     item.High,
			Low:      item.Low,
			Currency: currency,
			Type:     contractType,
		})
	}

	sort.Sort(newItems)
	buf, err := json.MarshalIndent(newItems, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	if true {
		fmt.Printf("%s\n", buf)
	}
	fmt.Printf("%d\n", len(newItems))
}

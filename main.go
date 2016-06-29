package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"text/tabwriter"
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

// CloseValues returns a slice of the ticks close value.
func (t Ticks) CloseValues() []float64 {
	ret := make([]float64, len(t))
	for i, tick := range t {
		ret[i] = tick.Close
	}
	return ret
}

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
func SimpleMovingAverage(ticks []float64, offset, period int) float64 {
	if period == 0 {
		panic(fmt.Errorf("can't average on 0 elements"))
	}
	if (offset+1)-period < 0 {
		return 0.
	}
	var sum float64
	for i := (offset + 1) - period; i <= offset; i++ {
		sum += ticks[i]
	}
	return sum / float64(period)
}

// EMA processes the Exponential Moving Average.
type EMA struct {
	ticks      []float64 // Tick list.
	emas       []float64 // EMA line.
	multiplier float64   // Multiplier (smoothing) constant.
	period     int       // Period for the given EMA.
}

// NewEMA instantiates a new EMA.
func NewEMA(ticks []float64, period int) EMA {
	return EMA{
		ticks:      ticks,
		emas:       make([]float64, len(ticks)),
		multiplier: 2. / float64(period+1),
		period:     period,
	}
}

// Compute computes the EMA at the given offset.
func (ema *EMA) Compute(offset int) float64 {
	if len(ema.ticks) < offset {
		panic(fmt.Errorf("EMA out of bounds"))
	}
	var ret float64

	// If not enough points, return 0.
	if (offset+1)-ema.period < 0 {
		return 0.
	} else if offset+1 == ema.period { // If len == period, then return SMA.
		ret = SimpleMovingAverage(ema.ticks, offset, ema.period)
	} else {
		ret = ema.multiplier*(ema.ticks[offset]-ema.emas[offset-1]) + ema.emas[offset-1]
	}
	ema.emas[offset] = ret
	return ret
}

// ComputeAll computes the EMA of all the known entries.
func (ema *EMA) ComputeAll() []float64 {
	ret := make([]float64, len(ema.ticks))
	for i := range ema.ticks {
		ret[i] = ema.Compute(i)
	}
	return ret
}

// Append appends the given tick to the list and process the EMA of the new offest.
func (ema *EMA) Append(tick float64) float64 {
	ema.ticks = append(ema.ticks, tick)
	ema.emas = append(ema.emas, 0.)
	return ema.Compute(len(ema.ticks) - 1)
}

// MACD processes the Moving Average Convergence Divergence indicator.
type MACD struct {
	ticks      []float64
	macdLine   []float64
	line1      EMA
	line2      EMA
	signalLine EMA
	histogram  []float64
}

// NewMACD instantiate a new MACD processor.
func NewMACD(ticks []float64, period1, period2, signalPeriod int) MACD {
	ema1 := NewEMA(ticks, period1)
	emaLine1 := ema1.ComputeAll()
	ema2 := NewEMA(ticks, period2)
	emaLine2 := ema2.ComputeAll()

	macdLine := make([]float64, len(ticks))
	for i := range macdLine {
		macdLine[i] = emaLine1[i] - emaLine2[i]
	}

	signalEMA := NewEMA(macdLine, signalPeriod)
	signalLine := signalEMA.ComputeAll()

	histogram := make([]float64, len(ticks))
	for i := range macdLine {
		histogram[i] = macdLine[i] - signalLine[i]
	}

	return MACD{
		ticks:      ticks,
		macdLine:   macdLine,
		line1:      ema1,
		line2:      ema2,
		signalLine: signalEMA,
		histogram:  histogram,
	}
}

// Append appends a new tick to the macd processor.
func (macd *MACD) Append(tick float64) {
	macd.ticks = append(macd.ticks, tick)
	ema1 := macd.line1.Append(tick)
	ema2 := macd.line2.Append(tick)
	macd.macdLine = append(macd.macdLine, ema1-ema2)
	signal := macd.signalLine.Append(ema1 - ema2)
	macd.histogram = append(macd.histogram, (ema1-ema2)-signal)
}

func main() {
	macd := NewMACD(nil, 12, 26, 9)

	buf, err := ioutil.ReadFile("b.json")
	if err != nil {
		log.Fatalf("Error loading json file: %s\n", err)
	}
	tt := Ticks{}
	if err := json.Unmarshal(buf, &tt); err != nil {
		log.Fatalf("Error parsing json file: %s\n", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 4, ' ', 0)
	fmt.Fprintf(w, "idx\tvalue\tMACD\tSignal\tHist\n")
	for i, t := range tt {
		macd.Append(t.Close)
		if i < 26+9 {
			continue
		}
		fmt.Fprintf(w, "%d\t%f\t%.2f\t%.2f\t%.2f\n", i+1, t.Close, macd.macdLine[i], macd.signalLine.emas[i], macd.histogram[i])

	}
	fmt.Fprintf(w, "\n")
	w.Flush()
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

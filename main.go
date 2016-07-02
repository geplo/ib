package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"sync"
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
			// tick, ok := event.(*ib.TickPrice)
			// if !ok {
			// 	continue
			// }
			// if tick.Type != ib.TickLast {
			// 	continue
			// }
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
	// Contract.
	Symbol   string `json:"symbol"`   // Symbol identifier of the tick.
	Currency string `json:"currency"` // Currency of the tick.
	Type     string `json:"type"`     // Type of contract of the tick (STK, OPT, FUT, IND, FOP, CASH, BAG, NEWS).

	// Time.
	Date time.Time `json:"date"` // Date of the tick.
	Span string    `json:"size"` // Span of the tick. // TODO: use int for seconds instead of string.

	// OHLC.
	Open  float64 `json:"open"`  // Open value of the tick.
	High  float64 `json:"high"`  // High value of the tick.
	Low   float64 `json:"low"`   // Low value of the tick.
	Close float64 `json:"close"` // Close value of the tick.

	// Extra data.
	Volume int64   `json:"volume"` // Trade volume for the time span.
	WAP    float64 `json:"wap"`    // Weighted average price for the time span.
	Count  int64   `json:"count"`  // Trade count for the time span.
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
	return t[i].Date.Before(t[j].Date)
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
	padding    int       // Initial padding to discard.
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
	if offset == 0 || (offset+1)-ema.period < ema.padding {
		return 0.
	} else if offset+1 == ema.period+ema.padding { // If len == period, then return SMA.
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

	// Metadata.
	period1      int
	period2      int
	signalPeriod int

	// Controls.
	sync.Mutex
}

// NewMACD instantiate a new MACD processor.
func NewMACD(ticks []float64, period1, period2, signalPeriod int) *MACD {
	macd := &MACD{
		ticks:        ticks,
		macdLine:     make([]float64, len(ticks)),
		line1:        NewEMA(ticks, period1),
		line2:        NewEMA(ticks, period2),
		histogram:    make([]float64, len(ticks)),
		period1:      period1,
		period2:      period2,
		signalPeriod: signalPeriod,
	}
	macd.ComputeAll()
	return macd
}

// ComputeAll computes the MACD for all the existing ticks.
func (macd *MACD) ComputeAll() {
	var (
		emaLine1 = macd.line1.ComputeAll()
		emaLine2 = macd.line2.ComputeAll()
	)

	macd.Lock()
	for i := range macd.macdLine {
		macd.macdLine[i] = emaLine1[i] - emaLine2[i]
	}

	macd.signalLine = NewEMA(macd.macdLine, macd.signalPeriod)
	macd.signalLine.padding = macd.period2 - 1
	signalLine := macd.signalLine.ComputeAll()

	for i := range macd.macdLine {
		macd.histogram[i] = macd.macdLine[i] - signalLine[i]
	}
	macd.Unlock()
}

// Append appends a new tick to the macd processor.
func (macd *MACD) Append(tick float64) {
	macd.Lock()
	macd.ticks = append(macd.ticks, tick)
	ema1 := macd.line1.Append(tick)
	ema2 := macd.line2.Append(tick)
	// As long as we don't have ema2, zero out the macd & co.
	if ema2 == 0 {
		ema1 = 0
	}
	macd.macdLine = append(macd.macdLine, ema1-ema2)
	signal := macd.signalLine.Append(ema1 - ema2)
	if len(macd.ticks) < macd.period2+macd.signalPeriod-1 {
		ema1 = 0
		ema2 = 0
	}
	macd.histogram = append(macd.histogram, (ema1-ema2)-signal)
	macd.Unlock()
}

func main() {
	macd := NewMACD(nil, 12, 26, 9)

	buf, err := ioutil.ReadFile("c.json")
	if err != nil {
		log.Fatalf("Error loading json file: %s\n", err)
	}
	tt := Ticks{}
	if err := json.Unmarshal(buf, &tt); err != nil {
		log.Fatalf("Error parsing json file: %s\n", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 4, ' ', 0)
	fmt.Fprintf(w, "idx\tvalue\t12 EMA\t26EMA\tMACD\tSignal\tHist\n")
	for i, t := range tt[:65] {
		macd.Append(t.Close)
		// fmt.Printf("%f\n", t.Close)
		// continue
		// if i < 26+9 {
		// 	continue
		// }
		fmt.Fprintf(w, "%d\t%.2f\t%f\t%f\t%f\t%f\t%f\n", i+1, t.Close, macd.line1.emas[i], macd.line2.emas[i], macd.macdLine[i], macd.signalLine.emas[i], macd.histogram[i])

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

// NYTZ is the New York timezone location. Used to display dates in that timezone.
var NYTZ *time.Location

func init() {
	tz, err := time.LoadLocation("America/New_York")
	if err != nil {
		log.Fatalf("Error initializing New York timezone: %s", err)
	}
	NYTZ = tz
}

func main11() {
	executorOptions := ib.EngineOptions{
		Gateway: "192.168.99.100:4004",
		//		Gateway:          "localhost:7497",
		DumpConversation: true,
	}
	stateChan := make(chan ib.EngineState)
	executorEngine, err := ib.NewEngine(executorOptions)
	if err != nil {
		log.Fatal(err)
	}
	executorEngine.SubscribeState(stateChan)

	subscriberOptions := ib.EngineOptions{
		Gateway: "192.168.99.100:4003",
		//Gateway:          "localhost:4001",
		DumpConversation: true,
	}
	subscriberEngine, err := ib.NewEngine(subscriberOptions)
	if err != nil {
		log.Fatal(err)
	}
	subscriberEngine.SubscribeState(stateChan)

	go func() {
		for state := range stateChan {
			log.Printf("--> State change: %s\n", state.String())
		}
	}()
	//	time.Sleep(2 * time.Second)

	requestAccountData := func() {
		eventChan := make(chan ib.Reply)
		go func() {
			for event := range eventChan {
				log.Printf("<-- Event: %#v\n", event)
			}
			println("______ END ______")
		}()

		nextID := subscriberEngine.NextRequestID()
		subscriberEngine.Subscribe(eventChan, nextID)

		fmt.Printf("Subscribed\n")

		req := &ib.RequestAccountSummary{
			Group: "All",
			Tags:  "AccountType,NetLiquidation,TotalCashValue,BuyingPower",
		}
		req.SetID(nextID)
		if err := subscriberEngine.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}
	}
	_ = requestAccountData
	//	requestAccountData()

	requestPositions := func() {
		eng := subscriberEngine

		eventChan := make(chan ib.Reply)

		positionChan := make(chan *ib.Position)
		go func() {
			defer close(positionChan)
			defer close(eventChan)

			for event := range eventChan {
				if position, ok := event.(*ib.Position); ok {
					positionChan <- position
				}
				if _, ok := event.(*ib.PositionEnd); ok {
					break
				}
			}
			eng.UnsubscribeAll(eventChan)
		}()

		eng.SubscribeAll(eventChan)

		req := &ib.RequestPositions{}
		if err := eng.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}

		positions := []*ib.Position{}
		for position := range positionChan {
			positions = append(positions, position)
		}

		buf, err := json.MarshalIndent(positions, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		if true {
			fmt.Printf("%s\n", buf)
		}
		println("=== End position ===")
	}
	_ = requestPositions
	requestPositions()

	//	subscribeTicker(subscriberEngine, NewDefaultStkContract("TSLA"))

	fct2 := func() {
		eventChan := make(chan ib.Reply)
		go func() {
			for event := range eventChan {
				log.Printf("<-- Event: %#v\n", event)
			}
		}()

		nextID := executorEngine.NextRequestID()
		executorEngine.Subscribe(eventChan, nextID)

		fmt.Printf("Subscribed\n")

		req := &ib.RequestContractData{
			Contract: NewDefaultStkContract("TSLA"),
		}
		req.SetID(nextID)
		if err := executorEngine.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}
	}
	_ = fct2

	fct3 := func() {
		eventChan := make(chan ib.Reply)
		go func() {
			for event := range eventChan {
				log.Printf("<-- Event: %#v\n", event)
			}
		}()

		nextID := executorEngine.NextRequestID()
		executorEngine.Subscribe(eventChan, nextID)

		fmt.Printf("Subscribed\n")

		req := &ib.RequestRealTimeBars{
			Contract:   NewDefaultStkContract("TSLA"),
			BarSize:    5,
			WhatToShow: ib.RealTimeTrades,
			UseRTH:     false,
		}
		req.SetID(nextID)
		if err := executorEngine.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}
	}
	_ = fct3

	fct4 := func() {
		eventChan := make(chan ib.Reply)
		go func() {
			for event := range eventChan {
				log.Printf("<-- Event: %#v\n", event)
			}
		}()

		nextID := executorEngine.NextRequestID()
		executorEngine.Subscribe(eventChan, nextID)

		fmt.Printf("Subscribed\n")

		order, err := ib.NewOrder()
		if err != nil {
			log.Fatalf("Error creating new order: %s\n", err)
		}
		order.OrderID = 103
		order.OrderType = "LIMIT"
		order.Action = "BUY"
		order.LimitPrice = 214.50
		order.MinQty = 100
		order.TotalQty = 100
		order.Transmit = true
		order.OutsideRTH = true

		req := &ib.PlaceOrder{
			Contract: NewDefaultStkContract("TSLA"),
			Order:    order,
		}
		req.SetID(nextID)
		if err := executorEngine.Send(req); err != nil {
			log.Fatalf("Error sending account summary request: %s\n", err)
		}
	}
	_ = fct4

	historicalFct := func() {
		var (
			symbol       = "AAPL"
			span         = ib.HistBarSize30Min
			currency     = "USD"
			contractType = "STK"
		)

		manager, err := ib.NewHistoricalDataManager(subscriberEngine, ib.RequestHistoricalData{
			Contract:    NewDefaultStkContract(symbol),
			Duration:    "1 M",
			BarSize:     ib.HistDataBarSize(span),
			WhatToShow:  ib.HistTrades,
			UseRTH:      false,
			EndDateTime: time.Date(2016, time.June, 25, 20, 00, 00, 00, NYTZ),
		})
		if err != nil {
			log.Fatal(err)
		}

		<-manager.Refresh()
		items := manager.Items()

		tickMapping := func(items []ib.HistoricalDataItem) Ticks {
			newItems := make(Ticks, 0, len(items))
			for _, item := range items {
				newItems = append(newItems, Tick{
					Symbol:   symbol,
					Currency: currency,
					Type:     contractType,

					Date: item.Date.In(NYTZ),
					Span: span,

					Open:  item.Open,
					Close: item.Close,
					High:  item.High,
					Low:   item.Low,

					Volume: item.Volume,
					WAP:    item.WAP,
					Count:  item.BarCount,
				})
			}
			return newItems
		}

		newItems := tickMapping(items)
		_ = newItems

		sort.Sort(newItems)
		buf, err := json.MarshalIndent(newItems, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		f, err := os.Create("c.json")
		if err != nil {
			log.Fatalf("Error creating json file: %s", err)
		}
		defer f.Close()

		fmt.Fprintf(f, "%s\n", buf)

		if true {
			fmt.Printf("%s\n", buf)
		}
		fmt.Printf("%d\n", len(newItems))
	}
	_ = historicalFct
	historicalFct()
}

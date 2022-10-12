package main

import (
	"context"
	"fmt"
	"github.com/filecoin-project/go-state-types/abi"
	types2 "github.com/filecoin-project/venus/venus-shared/types"
	"log"
	"strconv"
	"sync"

	"github.com/filecoin-project/go-state-types/big"
	v1 "github.com/filecoin-project/venus/venus-shared/api/chain/v1"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"
	"github.com/urfave/cli/v2"
	big2 "math/big"
	"net/http"
	"os"
)

var cache = NewGasPriceCache()
var cacheHead = sync.Map{}

const nblocksincl = 10

func main() {
	app := &cli.App{
		Name:  "premium monitor",
		Usage: "premium monitor",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "url",
				Value: "/ip4/192.168.200.21/tcp/1234",
				Usage: "节点地址",
			},
			&cli.StringFlag{
				Name:  "token",
				Value: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJBbGxvdyI6WyJyZWFkIiwid3JpdGUiLCJzaWduIiwiYWRtaW4iXX0.PDxOnfo_64ePKuJoM64hPu8pAJfT13sl-s7IysjQXSY",
				Usage: "节点token",
			},
			&cli.Float64Flag{
				Name:  "ratio",
				Value: 1.5,
				Usage: "建议值系数",
			},
			&cli.IntFlag{
				Name:  "rm-top",
				Value: 20,
				Usage: "移除最大的值，避免个别值影响",
			},
		},
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
		return
	}
}

func run(cctx *cli.Context) error {
	ctx := cctx.Context
	url := cctx.String("url")
	token := cctx.String("token")
	ratio := cctx.Float64("ratio")
	tailTop := cctx.Int("rm-top")
	api, closer, err := v1.DialFullNodeRPC(ctx, url, token, nil)
	if err != nil {
		return err
	}
	defer closer()
	h := Hander{
		ratio:   ratio,
		api:     api,
		tailTop: tailTop,
	}
	http.HandleFunc("/compare", h.compare)
	http.HandleFunc("/suggest", h.suggest)
	http.HandleFunc("/graph", h.graph)
	log.Print("listen at http://127.0.0.1:8081")
	log.Print("open http://127.0.0.1:8081/compare compare old and new premium")
	log.Print("open http://127.0.0.1:8081 website to explore premium")
	log.Print("access http://127.0.0.1:8081/suggest to get suggest value( this value select little top value)")
	return http.ListenAndServe(":8081", nil)
}

type Hander struct {
	ratio   float64
	tailTop int
	api     v1.FullNode
}

func (h Hander) suggest(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	height_req := req.URL.Query().Get("height")
	var ts *types2.TipSet
	var err error
	if height_req != "" {
		height, err := strconv.Atoi(height_req)
		if err != nil {
			log.Println("not illedge height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ts, err = h.api.ChainGetTipSetByHeight(ctx, abi.ChainEpoch(height), types2.EmptyTSK)
		if err != nil {
			log.Println("get tipset by height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	} else {
		ts, err = h.api.ChainHead(ctx)
		if err != nil {
			log.Println("get chain head", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	_, _, suggestPremium, err := getPremium(ctx, h.api, ts, h.ratio, h.tailTop)
	if err != nil {
		log.Println("get premium fail", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(suggestPremium.String()))
}

func (h Hander) graph(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	height_req := req.URL.Query().Get("height")
	var ts *types2.TipSet
	var err error
	if height_req != "" {
		height, err := strconv.Atoi(height_req)
		if err != nil {
			log.Println("not illedge height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ts, err = h.api.ChainGetTipSetByHeight(ctx, abi.ChainEpoch(height), types2.EmptyTSK)
		if err != nil {
			log.Println("get tipset by height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	} else {
		ts, err = h.api.ChainHead(ctx)
		if err != nil {
			log.Println("get chain head", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	priceList, officePremium, suggestPremium, err := getPremium(ctx, h.api, ts, h.ratio, h.tailTop)
	if err != nil {
		log.Println("get premium fail", err)
	}

	var x []string
	gasPremiumItems := make([]opts.LineData, 0)
	gasLimitItems := make([]opts.LineData, 0)
	count := 1
	for _, p := range priceList {
		x = append(x, strconv.Itoa(count))
		{
			item := opts.LineData{Value: p.Price.Int64()}
			item.SymbolSize = 5
			item.Symbol = "diamond"
			gasPremiumItems = append(gasPremiumItems, item)
			count++
		}
		{
			item := opts.LineData{Value: p.Limit}
			item.SymbolSize = 5
			item.Symbol = "diamond"
			gasLimitItems = append(gasLimitItems, item)
			count++
		}
	}
	// create a new line instance
	line := charts.NewLine()
	// set some global options like Title/Legend/ToolTip or anything else
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Premium chart produced by venus",
			Theme:     types.ThemeEssos,
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:      true,
			Trigger:   "axis",
			TriggerOn: "mousemove",
		}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type: "slider",
		}),
		charts.WithInitializationOpts(opts.Initialization{Theme: types.ThemeInfographic}),
		charts.WithTitleOpts(opts.Title{
			Title:    "Premium趋势图",
			Subtitle: fmt.Sprintf("office premium %s, suggest premium %s", officePremium.String(), suggestPremium.String()),
		}))

	// Put data into instance
	line.SetXAxis(x).AddSeries("Premium", gasPremiumItems).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: true}))

	line.SetXAxis(x).AddSeries("GasLimit", gasLimitItems).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: true}))
	_ = line.Render(w)
}

func (h Hander) compare(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	height_req := req.URL.Query().Get("height")
	var ts *types2.TipSet
	var err error
	if height_req != "" {
		height, err := strconv.Atoi(height_req)
		if err != nil {
			log.Println("not illedge height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ts, err = h.api.ChainGetTipSetByHeight(ctx, abi.ChainEpoch(height), types2.EmptyTSK)
		if err != nil {
			log.Println("get tipset by height", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	} else {
		ts, err = h.api.ChainHead(ctx)
		if err != nil {
			log.Println("get chain head", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	back := 10
	back_req := req.URL.Query().Get("back")
	if len(back_req) > 0 {
		back, err = strconv.Atoi(back_req)
		if err != nil {
			log.Println("not back ", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	data, err := getPremium_V2(ctx, h.api, ts, back)
	if err != nil {
		log.Println("get premium fail", err)
	}

	var x []string
	oldItems := make([]opts.LineData, 0)
	newItems := make([]opts.LineData, 0)
	for _, p := range data {
		x = append(x, strconv.Itoa(p.index))
		{
			item := opts.LineData{Value: p.OldPrice}
			item.SymbolSize = 10
			item.Symbol = "diamond"
			oldItems = append(oldItems, item)
		}
		{
			item := opts.LineData{Value: p.NewPrice}
			item.SymbolSize = 10
			item.Symbol = "diamond"
			newItems = append(newItems, item)
		}
	}
	// create a new line instance
	line := charts.NewLine()
	// set some global options like Title/Legend/ToolTip or anything else
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Premium chart produced by venus",
			Theme:     types.ThemeEssos,
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:      true,
			Trigger:   "axis",
			TriggerOn: "mousemove",
		}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type: "slider",
		}),
		charts.WithInitializationOpts(opts.Initialization{Theme: types.ThemeInfographic}),
		charts.WithTitleOpts(opts.Title{
			Title:    "Premium趋势图",
			Subtitle: fmt.Sprintf("from %s, to %s", 1, 1),
		}))

	// Put data into instance
	line.SetXAxis(x).AddSeries("Old", oldItems).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: true}))

	line.SetXAxis(x).AddSeries("New", newItems).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: true}))
	_ = line.Render(w)
}

func getPremium(ctx context.Context, api v1.FullNode, ts *types2.TipSet, ratio float64, rmTop int) ([]GasMeta, big.Int, big.Int, error) {
	officePirce, err := GasEstimateGasPremium(ctx, api, nblocksincl, cache, ts)
	if err != nil {
		return nil, big.Int{}, big.Int{}, err
	}

	pricesList, err := currentTopPrice(ctx, api, ts)
	if err != nil {
		return nil, big.Int{}, big.Int{}, err
	}

	floatPremium, _ := big2.NewFloat(0).SetString(pricesList[rmTop:][0].Price.String())
	suggetBigPremium, _ := floatPremium.Mul(floatPremium, big2.NewFloat(ratio)).Int(nil)
	return pricesList[rmTop:], officePirce, big.NewFromGo(suggetBigPremium), nil
}

type CompareValue struct {
	index    int
	OldPrice uint64
	NewPrice uint64
}

func getPremium_V2(ctx context.Context, api v1.FullNode, ts *types2.TipSet, back int) ([]CompareValue, error) {
	//2236115->2236122
	data := []CompareValue{}
	for i := 0; i <= back; i++ {
		officePrice, err := GasEstimateGasPremium(ctx, api, nblocksincl, cache, ts)
		if err != nil {
			return nil, err
		}

		newPrice, err := GasEstimateGasPremium_V2(ctx, api, 1000, cache, ts)
		if err != nil {
			return nil, err
		}
		data = append(data, CompareValue{index: int(ts.Height()), OldPrice: officePrice.Uint64(), NewPrice: newPrice.Uint64()})
		ts, err = api.ChainGetTipSet(ctx, ts.Parents())
		if err != nil {
			return nil, err
		}
	}

	return data, nil
}

func currentTopPrice(ctx context.Context, api v1.FullNode, ts *types2.TipSet) ([]GasMeta, error) {
	var prices []GasMeta
	var blocks int

	for i := uint64(0); i < nblocksincl*2; i++ {
		if ts.Height() == 0 {
			break // genesis
		}

		var pts *types2.TipSet
		if val, ok := cacheHead.Load(ts.Parents().String()); ok {
			pts = val.(*types2.TipSet)
		} else {
			var err error
			pts, err = api.ChainGetTipSet(ctx, ts.Parents())
			if err != nil {
				return nil, err
			}
			cacheHead.Store(ts.Parents().String(), pts)
		}

		blocks += len(pts.Blocks())
		meta, err := cache.GetTSGasStats(ctx, api, pts)
		if err != nil {
			return nil, err
		}
		prices = append(prices, meta...)

		ts = pts
	}

	selectPrices := selectMsgByGasPremium(prices, blocks)
	return selectPrices, nil
}

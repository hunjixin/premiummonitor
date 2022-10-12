package main

import (
	"context"
	"fmt"
	"github.com/filecoin-project/venus/pkg/constants"
	v1 "github.com/filecoin-project/venus/venus-shared/api/chain/v1"
	"sort"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	lru "github.com/hashicorp/golang-lru"

	"github.com/filecoin-project/venus/venus-shared/types"
)

func GasEstimateGasPremium_V2(
	ctx context.Context,
	api v1.FullNode,
	nmessageincl uint64,
	cache *GasPriceCache,
	ts *types.TipSet,
) (big.Int, error) {
	if nmessageincl == 0 {
		nmessageincl = 1
	}

	var prices []GasMeta
	var messages uint64
	var blocks int
	var tsIncl int
	for {
		if ts.Height() == 0 {
			break // genesis
		}

		var pts *types.TipSet
		if val, ok := cacheHead.Load(ts.Parents().String()); ok {
			pts = val.(*types.TipSet)
		} else {
			var err error
			pts, err = api.ChainGetTipSet(ctx, ts.Parents())
			if err != nil {
				return types.BigInt{}, err
			}
			cacheHead.Store(ts.Parents().String(), pts)
		}
		blocks += len(pts.Blocks())
		meta, err := cache.GetTSGasStats(ctx, api, pts)
		if err != nil {
			return types.BigInt{}, err
		}
		messages += uint64(len(meta))
		tsIncl += 1
		prices = append(prices, meta...)

		if messages > nmessageincl {
			break
		}
		ts = pts
	}

	premium := medianGasPremium(prices, blocks)

	if big.Cmp(premium, big.NewInt(MinGasPremium)) < 0 {
		switch tsIncl {
		case 1:
			premium = big.NewInt(2 * MinGasPremium)
		case 2:
			premium = big.NewInt(1.5 * MinGasPremium)
		default:
			premium = big.NewInt(MinGasPremium)
		}
	}

	return premium, nil
}

func GasEstimateGasPremium(
	ctx context.Context,
	api v1.FullNode,
	nblocksincl uint64,
	cache *GasPriceCache,
	ts *types.TipSet,
) (big.Int, error) {
	if nblocksincl == 0 {
		nblocksincl = 1
	}

	var prices []GasMeta
	var blocks int

	for i := uint64(0); i < nblocksincl*2; i++ {
		if ts.Height() == 0 {
			break // genesis
		}

		var pts *types.TipSet
		if val, ok := cacheHead.Load(ts.Parents().String()); ok {
			pts = val.(*types.TipSet)
		} else {
			var err error
			pts, err = api.ChainGetTipSet(ctx, ts.Parents())
			if err != nil {
				return types.BigInt{}, err
			}
			cacheHead.Store(ts.Parents().String(), pts)
		}

		blocks += len(pts.Blocks())
		meta, err := cache.GetTSGasStats(ctx, api, pts)
		if err != nil {
			return types.BigInt{}, err
		}
		prices = append(prices, meta...)

		ts = pts
	}

	premium := medianGasPremium(prices, blocks)

	if big.Cmp(premium, big.NewInt(MinGasPremium)) < 0 {
		switch nblocksincl {
		case 1:
			premium = big.NewInt(2 * MinGasPremium)
		case 2:
			premium = big.NewInt(1.5 * MinGasPremium)
		default:
			premium = big.NewInt(MinGasPremium)
		}
	}

	return premium, nil
}

// finds 55th percntile instead of median to put negative pressure on gas price
func selectMsgByGasPremium(prices []GasMeta, blocks int) []GasMeta {
	sort.Slice(prices, func(i, j int) bool {
		// sort desc by price
		return prices[i].Price.GreaterThan(prices[j].Price)
	})

	var result []GasMeta
	at := constants.BlockGasTarget * int64(blocks) / 2
	at += constants.BlockGasTarget * int64(blocks) / (2 * 20) // move 5% further
	for _, price := range prices {
		at -= price.Limit
		if at < 0 {
			break
		}
		result = append(result, price)
	}

	return result
}

// finds 55th percntile instead of median to put negative pressure on gas price
func medianGasPremium(prices []GasMeta, blocks int) abi.TokenAmount {
	sort.Slice(prices, func(i, j int) bool {
		// sort desc by price
		return prices[i].Price.GreaterThan(prices[j].Price)
	})

	at := constants.BlockGasTarget * int64(blocks) / 2
	at += constants.BlockGasTarget * int64(blocks) / (2 * 20) // move 5% further
	prev1, prev2 := big.Zero(), big.Zero()
	for _, price := range prices {
		prev1, prev2 = price.Price, prev1
		at -= price.Limit
		if at < 0 {
			break
		}
	}

	premium := prev1
	if prev2.Sign() != 0 {
		premium = big.Div(big.Add(prev1, prev2), big.NewInt(2))
	}

	return premium
}

const MinGasPremium = 100e3

// const MaxSpendOnFeeDenom = 100

type GasPriceCache struct {
	c *lru.TwoQueueCache
}

type GasMeta struct {
	Price big.Int
	Limit int64
}

func NewGasPriceCache() *GasPriceCache {
	// 50 because we usually won't access more than 40
	c, err := lru.New2Q(50)
	if err != nil {
		// err only if parameter is bad
		panic(err)
	}

	return &GasPriceCache{
		c: c,
	}
}

func (g *GasPriceCache) GetTSGasStats(ctx context.Context, provider v1.FullNode, ts *types.TipSet) ([]GasMeta, error) {
	i, has := g.c.Get(ts.Key())
	if has {
		return i.([]GasMeta), nil
	}

	var prices []GasMeta
	msgs, err := provider.ChainGetMessagesInTipset(ctx, ts.Key())
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	for _, msg := range msgs {
		prices = append(prices, GasMeta{
			Price: msg.Message.GasPremium,
			Limit: msg.Message.GasLimit,
		})
	}

	g.c.Add(ts.Key(), prices)

	return prices, nil
}

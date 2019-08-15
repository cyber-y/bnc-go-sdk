package rpc

import (
	"errors"
	"fmt"

	"github.com/binance-chain/go-sdk/common/types"
	"github.com/binance-chain/go-sdk/types/msg"
	"github.com/binance-chain/go-sdk/types/tx"
)

const (
	AccountStoreName = "acc"
	ParamABCIPrefix  = "param"
	TimeLockMsgRoute = "timelock"
	AtomicSwapStoreName = "atomic_swap"

	TimeLockrcNotFoundErrorCode = 458760
)

type DexClient interface {
	TxInfoSearch(query string, prove bool, page, perPage int) ([]tx.Info, error)
	ListAllTokens(offset int, limit int) ([]types.Token, error)
	GetTokenInfo(symbol string) (*types.Token, error)
	GetAccount(addr types.AccAddress) (acc types.Account, err error)
	GetCommitAccount(addr types.AccAddress) (acc types.Account, err error)

	GetBalances(addr types.AccAddress) ([]types.TokenBalance, error)
	GetBalance(addr types.AccAddress, symbol string) (*types.TokenBalance, error)
	GetFee() ([]types.FeeParam, error)
	GetOpenOrders(addr types.AccAddress, pair string) ([]types.OpenOrder, error)
	GetTradingPairs(offset int, limit int) ([]types.TradingPair, error)
	GetDepth(tradePair string, level int) (*types.OrderBook, error)
	GetProposals(status types.ProposalStatus, numLatest int64) ([]types.Proposal, error)
	GetProposal(proposalId int64) (types.Proposal, error)
	GetTimelocks(addr types.AccAddress) ([]types.TimeLockRecord, error)
	GetTimelock(addr types.AccAddress, recordID int64) (*types.TimeLockRecord, error)
	GetSwapByHash(randomNumberHash types.HexData) (types.AtomicSwap, error)
	GetSwapByCreator(creatorAddr string, swapStatus string, offset int64, limit int64) ([]types.AtomicSwap, error)
	GetSwapByRecipient(recipientAddr string, swapStatus string, offset int64, limit int64) ([]types.AtomicSwap, error)
}

func (c *HTTP) TxInfoSearch(query string, prove bool, page, perPage int) ([]tx.Info, error) {
	if err := ValidateTxSearchQueryStr(query); err != nil {
		return nil, err
	}
	return c.WSEvents.TxInfoSearch(query, prove, page, perPage)
}

func (c *HTTP) ListAllTokens(offset int, limit int) ([]types.Token, error) {
	if err := ValidateOffset(offset); err != nil {
		return nil, err
	}
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("tokens/list/%d/%d", offset, limit)
	result, err := c.ABCIQuery(path, nil)
	if err != nil {
		return nil, err
	}
	bz := result.Response.GetValue()
	tokens := make([]types.Token, 0)
	err = c.cdc.UnmarshalBinaryLengthPrefixed(bz, &tokens)
	return tokens, err
}

func (c *HTTP) GetTokenInfo(symbol string) (*types.Token, error) {
	if err := ValidateSymbol(symbol); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("tokens/info/%s", symbol)
	result, err := c.ABCIQuery(path, nil)
	if err != nil {
		return nil, err
	}
	bz := result.Response.GetValue()
	token := new(types.Token)
	err = c.cdc.UnmarshalBinaryLengthPrefixed(bz, token)
	return token, err
}

// Always fetch the account from the commit store at (currentHeight-1) in node.
// example:
// 1. currentCommitHeight: 1000, accountA(balance: 10BNB, sequence: 10)
// 2. height: 999, accountA(balance: 11BNB, sequence: 9)
// 3. GetCommitAccount will return accountA(balance: 11BNB, sequence: 9).
// If the (currentHeight-1) do not exist, will return  account at currentHeight.
// 1. currentCommitHeight: 1000, accountA(balance: 10BNB, sequence: 10)
// 2. height: 999. the state do not exist
// 3. GetCommitAccount will return accountA(balance: 10BNB, sequence: 10).
func (c *HTTP) GetCommitAccount(addr types.AccAddress) (acc types.Account, err error) {
	key := append([]byte("account:"), addr.Bytes()...)
	bz, err := c.QueryStore(key, AccountStoreName)
	if err != nil {
		return nil, err
	}
	if bz == nil {
		return nil, nil
	}
	err = c.cdc.UnmarshalBinaryBare(bz, &acc)
	if err != nil {
		return nil, err
	}
	return acc, err
}

// Always fetch the latest account from the cache in node even the previous transaction from
// this account is not included in block yet.
// example:
// 1. AccountA(Balance: 10BNB, sequence: 1), AccountB(Balance: 5BNB, sequence: 1)
// 2. Node receive Tx(AccountA --> AccountB 2BNB) and check have passed, but not included in block yet.
// 3. GetAccount will return AccountA(Balance: 8BNB, sequence: 2), AccountB(Balance: 7BNB, sequence: 1)
func (c *HTTP) GetAccount(addr types.AccAddress) (acc types.Account, err error) {
	result, err := c.ABCIQuery(fmt.Sprintf("/account/%s", addr.String()), nil)
	if err != nil {
		return nil, err
	}
	resp := result.Response
	if !resp.IsOK() {
		return nil, errors.New(resp.Log)
	}
	value := result.Response.GetValue()
	if len(value) == 0 {
		return nil, nil
	}
	err = c.cdc.UnmarshalBinaryBare(value, &acc)
	if err != nil {
		return nil, err
	}
	return acc, err
}

func (c *HTTP) GetBalances(addr types.AccAddress) ([]types.TokenBalance, error) {
	account, err := c.GetAccount(addr)
	if err != nil {
		return nil, err
	}
	coins := account.GetCoins()

	symbs := make([]string, 0, len(coins))
	bals := make([]types.TokenBalance, 0, len(coins))
	for _, coin := range coins {
		symbs = append(symbs, coin.Denom)
		// count locked and frozen coins
		var locked, frozen int64
		nacc := account.(types.NamedAccount)
		if nacc != nil {
			locked = nacc.GetLockedCoins().AmountOf(coin.Denom)
			frozen = nacc.GetFrozenCoins().AmountOf(coin.Denom)
		}
		bals = append(bals, types.TokenBalance{
			Symbol: coin.Denom,
			Free:   types.Fixed8(coins.AmountOf(coin.Denom)),
			Locked: types.Fixed8(locked),
			Frozen: types.Fixed8(frozen),
		})
	}
	return bals, nil
}

func (c *HTTP) GetBalance(addr types.AccAddress, symbol string) (*types.TokenBalance, error) {
	if err := ValidateSymbol(symbol); err != nil {
		return nil, err
	}
	exist := c.existsCC(symbol)
	if !exist {
		return nil, errors.New("symbol not found")
	}
	acc, err := c.GetAccount(addr)
	if err != nil {
		return nil, err
	}
	var locked, frozen int64
	nacc := acc.(types.NamedAccount)
	if nacc != nil {
		locked = nacc.GetLockedCoins().AmountOf(symbol)
		frozen = nacc.GetFrozenCoins().AmountOf(symbol)
	}
	return &types.TokenBalance{
		Symbol: symbol,
		Free:   types.Fixed8(nacc.GetCoins().AmountOf(symbol)),
		Locked: types.Fixed8(locked),
		Frozen: types.Fixed8(frozen),
	}, nil
}

func (c *HTTP) GetFee() ([]types.FeeParam, error) {
	rawFee, err := c.ABCIQuery(fmt.Sprintf("%s/fees", ParamABCIPrefix), nil)
	if err != nil {
		return nil, err
	}
	var fees []types.FeeParam
	err = c.cdc.UnmarshalBinaryLengthPrefixed(rawFee.Response.GetValue(), &fees)
	return fees, err
}

func (c *HTTP) GetOpenOrders(addr types.AccAddress, pair string) ([]types.OpenOrder, error) {
	if err := ValidatePair(pair); err != nil {
		return nil, err
	}
	rawOrders, err := c.ABCIQuery(fmt.Sprintf("dex/openorders/%s/%s", pair, addr), nil)
	if err != nil {
		return nil, err
	}
	bz := rawOrders.Response.GetValue()
	openOrders := make([]types.OpenOrder, 0)
	if bz == nil {
		return openOrders, nil
	}
	if err := c.cdc.UnmarshalBinaryLengthPrefixed(bz, &openOrders); err != nil {
		return nil, err
	} else {
		return openOrders, nil
	}
}

func (c *HTTP) GetTradingPairs(offset int, limit int) ([]types.TradingPair, error) {
	if err := ValidateLimit(limit); err != nil {
		return nil, err
	}
	if err := ValidateOffset(offset); err != nil {
		return nil, err
	}
	rawTradePairs, err := c.ABCIQuery(fmt.Sprintf("dex/pairs/%d/%d", offset, limit), nil)
	if err != nil {
		return nil, err
	}
	pairs := make([]types.TradingPair, 0)
	if rawTradePairs.Response.GetValue() == nil {
		return pairs, nil
	}
	err = c.cdc.UnmarshalBinaryLengthPrefixed(rawTradePairs.Response.GetValue(), &pairs)
	return pairs, err
}

func (c *HTTP) GetDepth(tradePair string, level int) (*types.OrderBook, error) {
	if err := ValidatePair(tradePair); err != nil {
		return nil, err
	}
	if err := ValidateDepthLevel(level); err != nil {
		return nil, err
	}
	rawDepth, err := c.ABCIQuery(fmt.Sprintf("dex/orderbook/%s/%d", tradePair, level), nil)
	if err != nil {
		return nil, err
	}
	var ob types.OrderBook
	err = c.cdc.UnmarshalBinaryLengthPrefixed(rawDepth.Response.GetValue(), &ob)
	if err != nil {
		return nil, err
	}
	return &ob, nil
}

func (c *HTTP) GetTimelocks(addr types.AccAddress) ([]types.TimeLockRecord, error) {

	params := types.QueryTimeLocksParams{
		Account: addr,
	}

	bz, err := c.cdc.MarshalJSON(params)

	if err != nil {
		fmt.Errorf("marshal params failed %v", err)
	}

	rawRecords, err := c.ABCIQuery(fmt.Sprintf("custom/%s/%s", TimeLockMsgRoute, "timelocks"), bz)

	if err != nil {
		return nil, err
	}
	if rawRecords == nil {
		return nil, fmt.Errorf("zero records")
	}
	records := make([]types.TimeLockRecord, 0)

	if err = c.cdc.UnmarshalJSON(rawRecords.Response.GetValue(), &records); err != nil {
		return nil, err
	} else {
		return records, nil
	}

}

func (c *HTTP) GetTimelock(addr types.AccAddress, recordID int64) (*types.TimeLockRecord, error) {

	params := types.QueryTimeLockParams{
		Account: addr,
		Id:      recordID,
	}

	bz, err := c.cdc.MarshalJSON(params)

	if err != nil {
		return nil, fmt.Errorf("incorrectly formatted request data %s", err.Error())
	}

	rawRecord, err := c.ABCIQuery(fmt.Sprintf("custom/%s/%s", TimeLockMsgRoute, "timelock"), bz)

	if err != nil {
		return nil, fmt.Errorf("error query %s", err.Error())
	}
	if rawRecord.Response.Code == TimeLockrcNotFoundErrorCode {
		return nil, nil
	}
	var record types.TimeLockRecord

	err = c.cdc.UnmarshalJSON(rawRecord.Response.GetValue(), &record)
	if err != nil {
		return nil, err
	}

	return &record, nil

}

func (c *HTTP) GetProposals(status types.ProposalStatus, numLatest int64) ([]types.Proposal, error) {
	params := types.QueryProposalsParams{}
	if status != types.StatusNil {
		params.ProposalStatus = status
	}
	if numLatest > 0 {
		params.NumLatestProposals = numLatest
	}

	bz, err := c.cdc.MarshalJSON(&params)
	if err != nil {
		return nil, err
	}
	rawProposals, err := c.ABCIQuery("custom/gov/proposals", bz)
	if err != nil {
		return nil, err
	}
	proposals := make([]types.Proposal, 0)

	err = c.cdc.UnmarshalJSON(rawProposals.Response.GetValue(), &proposals)
	return proposals, err
}

func (c *HTTP) GetProposal(proposalId int64) (types.Proposal, error) {
	params := types.QueryProposalParams{
		ProposalID: proposalId,
	}
	bz, err := c.cdc.MarshalJSON(params)
	if err != nil {
		return nil, err
	}
	fmt.Println(string(bz))
	rawProposals, err := c.ABCIQuery("custom/gov/proposal", bz)
	if err != nil {
		return nil, err
	}
	var proposal types.Proposal

	err = c.cdc.UnmarshalJSON(rawProposals.Response.GetValue(), &proposal)
	return proposal, err
}

func (c *HTTP) existsCC(symbol string) bool {
	resp, err := c.ABCIQuery(fmt.Sprintf("tokens/info/%s", symbol), nil)
	if err != nil {
		return false
	}
	if !resp.Response.IsOK() {
		return false
	}
	if len(resp.Response.GetValue()) == 0 {
		return false
	}
	var token types.Token
	err = c.cdc.UnmarshalBinaryLengthPrefixed(resp.Response.GetValue(), &token)
	if err != nil {
		return false
	}
	return true
}

func (c *HTTP) GetSwapByHash(randomNumberHash types.HexData) (types.AtomicSwap, error) {
	hashKey := []byte{0x01}
	hashKey = append(hashKey, randomNumberHash...)

	res, err := c.QueryStore(hashKey, AtomicSwapStoreName)
	if err != nil {
		return types.AtomicSwap{}, err
	}

	if res == nil {
		return types.AtomicSwap{}, fmt.Errorf("no matched swap record")
	}

	var atomicSwap types.AtomicSwap
	err = c.cdc.UnmarshalBinaryBare(res, &atomicSwap)
	if err != nil {
		return types.AtomicSwap{}, err
	}

	return atomicSwap, nil
}

func (c *HTTP) GetSwapByCreator(creatorAddr string, swapStatus string, offset int64, limit int64) ([]types.AtomicSwap, error) {
	addr, err := types.AccAddressFromBech32(creatorAddr)
	if err != nil {
		return nil, err
	}

	status := types.NewSwapStatusFromString(swapStatus)
	params := types.QuerySwapByCreatorParams{
		Creator: addr,
		Status:  status,
		Limit:   limit,
		Offset:  offset,
	}

	bz, err := c.cdc.MarshalJSON(params)
	if err != nil {
		return nil, err
	}

	resp, err := c.ABCIQuery(fmt.Sprintf("custom/%s/%s", msg.AtomicSwapRoute, "swapcreator"), bz)
	if err != nil {
		return nil, err
	}
	var result []types.AtomicSwap
	err = c.cdc.UnmarshalJSON(resp.Response.GetValue(), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *HTTP) GetSwapByRecipient(recipientAddr string, swapStatus string, offset int64, limit int64) ([]types.AtomicSwap, error) {
	recipient, err := types.AccAddressFromBech32(recipientAddr)
	if err != nil {
		return nil, err
	}
	status := types.NewSwapStatusFromString(swapStatus)
	params := types.QuerySwapByRecipientParams{
		Recipient: recipient,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	}

	bz, err := c.cdc.MarshalJSON(params)
	if err != nil {
		return nil, err
	}

	resp, err := c.ABCIQuery(fmt.Sprintf("custom/%s/%s", msg.AtomicSwapRoute, "swaprecipient"), bz)
	if err != nil {
		return nil, err
	}
	var result []types.AtomicSwap
	err = c.cdc.UnmarshalJSON(resp.Response.GetValue(), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

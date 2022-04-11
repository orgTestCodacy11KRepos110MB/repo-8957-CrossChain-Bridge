package terra

import (
	"fmt"
	"math/big"
	"time"

	"github.com/anyswap/CrossChain-Bridge/common"
	"github.com/anyswap/CrossChain-Bridge/tokens"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
)

var (
	retryRPCCount    = 3
	retryRPCInterval = 1 * time.Second

	signMode = signing.SignMode_SIGN_MODE_DIRECT
)

// BuildRawTransaction build raw tx
func (b *Bridge) BuildRawTransaction(args *tokens.BuildTxArgs) (rawTx interface{}, err error) {
	pairID := args.PairID
	tokenCfg := b.GetTokenConfig(pairID)
	if tokenCfg == nil {
		return nil, fmt.Errorf("swap pair '%v' is not configed", pairID)
	}

	var (
		from   string
		to     string
		amount *big.Int
		memo   string
	)

	switch args.SwapType {
	case tokens.SwapinType:
		return nil, tokens.ErrSwapTypeNotSupported
	case tokens.SwapoutType:
		from = tokenCfg.DcrmAddress // from
		to = args.Bind              //to

		amount = tokens.CalcSwappedValue(pairID, args.OriginValue, false, args.OriginFrom, args.OriginTxTo) // amount
		memo = tokens.UnlockMemoPrefix + args.SwapID
	default:
		return nil, tokens.ErrUnknownSwapType
	}

	if from == "" {
		return nil, tokens.ErrTxWithWrongSender
	}

	err = b.checkTokenBalance(tokenCfg.ContractAddress, from, amount)
	if err != nil {
		return nil, err
	}

	extra, err := b.initExtra(args)
	if err != nil {
		return nil, err
	}

	txb := newBuilder()

	txb.SetSignerData(
		b.ChainConfig.GetChainID().String(),
		tokenCfg.DcrmAccountNumber,
		*extra.Sequence)

	txb.SetMemo(memo)

	txb.SetGasLimit(*extra.Gas)

	parsedFees, err := sdk.ParseCoinsNormalized(*extra.Fees)
	if err != nil {
		return nil, err
	}
	txb.SetFeeAmount(parsedFees)

	feePayer, err := sdk.AccAddressFromBech32(tokenCfg.DcrmAddress)
	if err != nil {
		return nil, err
	}
	txb.SetFeePayer(feePayer)

	execMsg, err := GetTokenTransferExecMsg(to, amount.String())
	if err != nil {
		return nil, err
	}
	msg := NewMsgExecuteContract(from, tokenCfg.ContractAddress, execMsg)
	txb.SetMsgs(msg)

	pubkey, err := PubKeyFromStr(tokenCfg.DcrmPubkey)
	if err != nil {
		return nil, err
	}

	sigData := signing.SingleSignatureData{
		SignMode:  signMode,
		Signature: nil,
	}
	sig := signing.SignatureV2{
		PubKey:   pubkey,
		Data:     &sigData,
		Sequence: *extra.Sequence,
	}
	txb.SetSignatures(sig)

	return txb, nil
}

func (b *Bridge) initExtra(args *tokens.BuildTxArgs) (extra *tokens.TerraExtra, err error) {
	extra = getOrInitExtra(args)
	if extra.Sequence == nil {
		extra.Sequence, err = b.getSequence(args)
		if err != nil {
			return nil, err
		}
	}
	if extra.Fees == nil {
	}
	if extra.Gas == nil {
	}
	return extra, tokens.ErrTodo
}

func (b *Bridge) getMinReserveFee() *big.Int {
	minReserveFee := b.ChainConfig.GetMinReserveFee()
	if minReserveFee == nil {
		minReserveFee = big.NewInt(0)
	}
	return minReserveFee
}

func (b *Bridge) getSequence(args *tokens.BuildTxArgs) (*uint64, error) {
	var sequence uint64
	var err error
	for i := 0; i < retryRPCCount; i++ {
		sequence, err = b.GetAccountSequence(args.From)
		if err == nil {
			break
		}
		time.Sleep(retryRPCInterval)
	}
	if err != nil {
		return nil, err
	}
	if args.SwapType != tokens.NoSwapType {
		tokenCfg := b.GetTokenConfig(args.PairID)
		if tokenCfg != nil && args.From == tokenCfg.DcrmAddress {
			sequence = b.AdjustNonce(args.PairID, sequence)
		}
	}
	return &sequence, nil
}

func getOrInitExtra(args *tokens.BuildTxArgs) *tokens.TerraExtra {
	if args.Extra == nil || args.Extra.TerraExtra == nil {
		args.Extra = &tokens.AllExtras{TerraExtra: &tokens.TerraExtra{}}
	}
	return args.Extra.TerraExtra
}

// GetPoolNonce impl NonceSetter interface
func (b *Bridge) GetPoolNonce(address, _height string) (uint64, error) {
	return b.GetAccountSequence(address)
}

// GetAccountSequence get account sequence
func (b *Bridge) GetAccountSequence(address string) (uint64, error) {
	urls := append(b.GatewayConfig.APIAddress, b.GatewayConfig.APIAddressExt...)
	var acc *BaseAccount
	var err error
	for _, url := range urls {
		acc, err = GetBaseAccount(url, address)
		if err == nil && acc != nil {
			return common.GetUint64FromStr(acc.Sequence)
		}
	}
	return 0, wrapRPCQueryError(err, "GetAccountSequence")
}

// GetAccountNumber get account number
func (b *Bridge) GetAccountNumber(address string) (uint64, error) {
	urls := append(b.GatewayConfig.APIAddress, b.GatewayConfig.APIAddressExt...)
	var acc *BaseAccount
	var err error
	for _, url := range urls {
		acc, err = GetBaseAccount(url, address)
		if err == nil && acc != nil {
			return common.GetUint64FromStr(acc.AccountNumber)
		}
	}
	return 0, wrapRPCQueryError(err, "GetAccountNumber")
}

func (b *Bridge) checkCoinBalance(account, denom string, amount *big.Int) error {
	bal, err := b.GetBalanceByDenom(account, denom)
	if err != nil {
		return err
	}
	if bal.BigInt().Cmp(amount) < 0 {
		return fmt.Errorf(
			"insufficient native balance, sender: %v, %v < %v",
			account, bal, amount)
	}
	return nil
}

func (b *Bridge) checkTokenBalance(token, account string, amount *big.Int) error {
	bal, err := b.GetTokenBalance(token, account)
	if err != nil {
		return err
	}
	if bal.BigInt().Cmp(amount) < 0 {
		return fmt.Errorf(
			"insufficient %v balance, account: %v,  %v < %v",
			token, account, bal, amount)
	}
	return nil
}
package executor

import (
	"vaultdb/internal/core/executor/types"
)

func init() {
	types.EvalOperandFn = evalOperand
	types.EvalExprFn = evalExpr
	types.EvaluateCheckExprFn = evaluateCheckExpr
	types.FreezeInsertFn = freezeInsert
	types.FreezeUpdateFn = freezeUpdate
	types.FreezeDeleteFn = freezeDelete
	types.ApplyTxOverlayFn = applyTxOverlay
	types.MutateUnderTableLockFn = mutateUnderTableLock
	types.FireTriggersFn = fireTriggers
	types.NotifyBroadcasterFn = notifyBroadcaster
	types.ExecuteSelectWithCTEFn = ExecuteSelectWithCTE
}

// notifyBroadcaster notifies the broadcaster about a table mutation.
// This handles the nil-check for the concrete Broadcaster type.
func notifyBroadcaster(ctx *types.ExecutionContext, dbName, tableName string) {
	if b, ok := ctx.Broadcaster.(*Broadcaster); ok && b != nil {
		b.NotifyTableChanged(dbName, tableName, ctx)
	}
}

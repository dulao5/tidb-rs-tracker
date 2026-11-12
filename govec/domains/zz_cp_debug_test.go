package domains

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/types"
)

func TestCPDebug(t *testing.T) {
	raw, _ := os.ReadFile("/tmp/cp_seed_fail.json")
	var p []json.RawMessage
	_ = json.Unmarshal(raw, &p)
	ctx, _ := castCtx(p[3])
	ev := ctx.GetEvalCtx()
	fts := []*types.FieldType{}
	for _, f := range exArr(p[4]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	conds := evExprs(ctx, p[1], fts)
	k := conds[0].(*expression.ScalarFunction).GetArgs()[1]
	fmt.Println("K", k.StringWithCtx(ev, "off"), k.GetType(ev))
	c2 := conds[1].(*expression.ScalarFunction)
	fmt.Println("cond2", c2.StringWithCtx(ev, "off"), c2.GetArgs()[1].GetType(ev))
	col := &expression.Column{UniqueID: 2, Index: 1, RetType: fts[1], OrigName: "c"}
	r := expression.ColumnSubstitute(ctx, c2, expression.NewSchema(col), []expression.Expression{k})
	sf := r.(*expression.ScalarFunction)
	fmt.Println("res", r.StringWithCtx(ev, "off"), sf.GetArgs()[1].GetType(ev))
	inner := c2.GetArgs()[1].(*expression.ScalarFunction)
	e, err := expression.BuildCastFunctionWithCheck(ctx, k, inner.RetType.Clone(), false, false)
	fmt.Println("cast", e.StringWithCtx(ev, "off"), e.GetType(ev), err)
}

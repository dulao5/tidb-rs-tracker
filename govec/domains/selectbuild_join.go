package domains

// The joins of domain "selectbuild" (tidb-rs specs/D09.3-join-build spec §6.2, join plan
// building part A): the fixed join statements of the "build" op, and op "joinop", which builds
// a statement, takes its first LogicalJoin and calls one of the operator's methods.
//
// Records:
//   - "joinop": args [ddls, sql, spec] (spec {"kind", ...}) -> by kind:
//     "inspect" {"types": [[JoinType.String, IsOuterJoin, IsSemiJoin] for the 7 join types],
//     "keys": [left uids, right uids, isNullEQ, hasNullEQ], "na_keys", "naaj", "prefer_any":
//     [PreferAny([flag]) for spec.flags], "part_keys": [[[uid, collate id]], ...],
//     "schemas": [BuildLogicalJoinSchema(t, join) for the 7 join types]};
//     "keyinfo" (spec.left_keys / right_keys: the children's keys as column offsets,
//     spec.join_type: the join type to use, -1 to keep) {"keys": [[uid]], "max_one_row"};
//     "attach" (spec.join_type) the join after AttachOnConds(the conditions of the Selection
//     right above it) with that join type;
//     "extract" (spec.derive_left / derive_right) the four lists ExtractOnCondition returns
//     for the conditions of the Selection above the join;
//     "hints" (spec.left / right: LeftPreferJoinType / RightPreferJoinType) {"prefer", "w"}
//     after SetPreferredJoinType;
//     "replace" the join after ReplaceExprColumns(left child's first column -> a clone with
//     unique id 9999).

import (
	"context"
	"fmt"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	utilhint "github.com/pingcap/tidb/pkg/util/hint"
)

// sbJoinFixed: the join statements of the "build" op (each in both modes, like sbFixed). The
// tables are those of sbDDLs: t2 (a, b, c, d, e; no primary key), tc (a, b, c; clustered
// primary key (b, a), so tc.a / tc.b are NOT NULL), ti (a, b, c), t3 (x, y NOT NULL, z;
// non-clustered primary key), sbtest1.
var sbJoinFixed = []string{
	// Join kinds.
	"SELECT * FROM t2 JOIN tc",
	"SELECT * FROM t2 INNER JOIN tc ON t2.a = tc.a",
	"SELECT * FROM t2 CROSS JOIN tc",
	"SELECT * FROM t2, tc WHERE t2.a = tc.a",
	"SELECT * FROM t2 STRAIGHT_JOIN tc ON t2.a = tc.a",
	"SELECT STRAIGHT_JOIN * FROM t2 JOIN tc ON t2.a = tc.a",
	"SELECT STRAIGHT_JOIN * FROM t2, tc, ti",
	// Outer joins: the NOT NULL flag of the inner side is cleared.
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a",
	"SELECT * FROM t2 LEFT OUTER JOIN tc ON t2.a = tc.a AND tc.c > 1",
	"SELECT * FROM t2 RIGHT JOIN tc ON t2.a = tc.a",
	"SELECT * FROM tc RIGHT OUTER JOIN t3 ON tc.b = t3.x",
	"SELECT * FROM tc LEFT JOIN t3 ON tc.b = t3.x",
	"SELECT tc.a, t3.y FROM tc LEFT JOIN t3 ON tc.a = t3.y",
	"SELECT tc.a, t3.y FROM tc RIGHT JOIN t3 ON tc.a = t3.y",
	// ON conditions.
	"SELECT * FROM t2 JOIN tc ON t2.a > tc.a",
	"SELECT * FROM t2 JOIN tc ON t2.a = 1",
	"SELECT * FROM t2 JOIN tc ON 1 = 1",
	"SELECT * FROM t2 JOIN tc ON t2.a = tc.a AND t2.c < tc.c AND tc.b = 'x'",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a AND t2.c < tc.c AND tc.b = 'x' AND t2.d > 0",
	"SELECT * FROM t2 RIGHT JOIN tc ON t2.a = tc.a AND t2.c < tc.c AND tc.b = 'x' AND t2.d > 0",
	"SELECT * FROM t2 LEFT JOIN tc ON 1",
	"SELECT * FROM t2 LEFT JOIN tc ON NULL",
	"SELECT * FROM t2 LEFT JOIN tc ON 0",
	"SELECT * FROM t2 RIGHT JOIN tc ON t2.a = 1",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a + 1 = tc.a",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a <=> tc.a",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a OR t2.b = tc.b",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.b = tc.b",
	"SELECT * FROM t3 LEFT JOIN tc ON t3.x = tc.b",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a WHERE tc.a IS NULL",
	"SELECT * FROM t2 JOIN tc ON t2.a = tc.a LIMIT 1",
	"SELECT * FROM t2 JOIN tc ON tc.x = 1",
	"SELECT * FROM t2 JOIN tc ON t2.a IN (SELECT y FROM t3)",
	// USING.
	"SELECT * FROM t2 JOIN tc USING (a)",
	"SELECT * FROM t2 JOIN tc USING (a, b)",
	"SELECT * FROM t2 JOIN tc USING (b, a)",
	"SELECT * FROM t2 JOIN tc USING (a, a)",
	"SELECT * FROM t2 LEFT JOIN tc USING (a)",
	"SELECT * FROM t2 RIGHT JOIN tc USING (a)",
	"SELECT * FROM t2 RIGHT JOIN tc USING (b, a)",
	"SELECT * FROM t2 JOIN tc USING (x)",
	"SELECT * FROM t2 JOIN tc USING (a, x)",
	"SELECT * FROM t2 JOIN tc USING (_tidb_rowid)",
	"SELECT * FROM t2 JOIN t3 USING (_tidb_rowid)",
	"SELECT * FROM t2 JOIN tc USING (a) JOIN ti USING (a)",
	"SELECT * FROM t2 JOIN tc USING (a) JOIN ti USING (b)",
	"SELECT * FROM t2 JOIN (tc JOIN ti USING (a)) USING (a)",
	"SELECT * FROM t2 LEFT JOIN (tc JOIN ti USING (a)) USING (a)",
	"SELECT * FROM sbtest1 s1 JOIN sbtest1 s2 USING (id)",
	"SELECT * FROM sbtest1 s1 LEFT JOIN sbtest1 s2 USING (k)",
	// NATURAL.
	"SELECT * FROM t2 NATURAL JOIN tc",
	"SELECT * FROM t2 NATURAL LEFT JOIN tc",
	"SELECT * FROM t2 NATURAL RIGHT JOIN tc",
	"SELECT * FROM t2 NATURAL JOIN t3",
	"SELECT * FROM t2 NATURAL JOIN tc NATURAL JOIN ti",
	"SELECT * FROM t2 JOIN tc ON t2.a = tc.a NATURAL JOIN ti",
	"SELECT * FROM sbtest1 s1 NATURAL JOIN sbtest1 s2",
	// The full schema of a USING / NATURAL join.
	"SELECT t2.*, tc.* FROM t2 JOIN tc USING (b)",
	"SELECT tc.* FROM t2 JOIN tc USING (a)",
	"SELECT tc.*, t2.* FROM t2 NATURAL JOIN tc",
	"SELECT tc.a, t2.a, a FROM t2 JOIN tc USING (a)",
	"SELECT tc.a FROM t2 JOIN tc USING (a) WHERE tc.a > 0",
	"SELECT tc.a FROM t2 RIGHT JOIN tc USING (a) WHERE t2.a > 0",
	"SELECT test.tc.a FROM t2 JOIN tc USING (a)",
	"SELECT a FROM t2 JOIN tc ON t2.a = tc.a",
	"SELECT t3._tidb_rowid FROM t2 JOIN t3 ON t2.a = t3.y",
	"SELECT _tidb_rowid FROM t2 JOIN t3 ON t2.a = t3.y",
	// ONLY_FULL_GROUP_BY, HAVING / ORDER BY through the join.
	"SELECT t2.a, COUNT(*) FROM t2 JOIN tc USING (a) GROUP BY tc.a",
	"SELECT tc.a, COUNT(*) FROM t2 JOIN tc USING (a) GROUP BY a",
	"SELECT a, COUNT(*) FROM t2 JOIN tc USING (a) GROUP BY a",
	"SELECT tc.c FROM t2 JOIN tc ON t2.a = tc.a GROUP BY t2.a",
	"SELECT tc.c FROM t2 JOIN tc ON tc.b = t2.b AND tc.a = t2.a GROUP BY t2.a, t2.b",
	"SELECT tc.c FROM t2 LEFT JOIN tc ON tc.b = t2.b AND tc.a = t2.a GROUP BY t2.a, t2.b",
	"SELECT t2.c FROM t2 RIGHT JOIN tc ON tc.b = t2.b AND tc.a = t2.a GROUP BY tc.a, tc.b",
	"SELECT tc.c FROM tc JOIN t2 ON tc.b = t2.b AND tc.a = t2.a GROUP BY t2.a, t2.b",
	"SELECT tc.c FROM t2 JOIN tc USING (a, b) GROUP BY a, b",
	"SELECT tc.c FROM t2 JOIN tc USING (a, b) GROUP BY t2.a, t2.b",
	"SELECT tc.c FROM t2 JOIN tc WHERE tc.b = t2.b AND tc.a = t2.a GROUP BY t2.a, t2.b",
	"SELECT tc.c, t2.d FROM t2 JOIN tc ON tc.b = t2.b AND tc.a = t2.a",
	"SELECT tc.c, COUNT(*) FROM t2 JOIN tc ON tc.b = t2.b AND tc.a = t2.a",
	"SELECT a FROM t2 JOIN tc USING (a) HAVING tc.a > 0",
	"SELECT a FROM t2 JOIN tc USING (a) ORDER BY tc.a",
	"SELECT t2.a FROM t2 JOIN tc USING (a) ORDER BY tc.a",
	"SELECT COUNT(*) FROM t2 JOIN tc USING (a) ORDER BY tc.a",
	"SELECT COUNT(*) FROM t2 JOIN tc USING (a) HAVING tc.a > 0",
	"SELECT a, SUM(t2.c) FROM t2 JOIN tc USING (a) GROUP BY tc.a ORDER BY t2.a",
	"SELECT DISTINCT tc.a FROM t2 JOIN tc USING (a)",
	"SELECT t2.a, ROW_NUMBER() OVER (ORDER BY tc.a) FROM t2 JOIN tc USING (a)",
	// Nested and multi-table joins.
	"SELECT * FROM t2 JOIN (tc JOIN ti ON tc.a = ti.a) ON t2.a = tc.a",
	"SELECT * FROM t2 JOIN tc ON t2.a = tc.a JOIN ti ON ti.a = tc.a AND ti.b = t2.a",
	"SELECT * FROM (t2, tc) JOIN ti ON ti.a = t2.a",
	"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a LEFT JOIN ti ON ti.a = tc.a",
	"SELECT * FROM t2 RIGHT JOIN tc ON t2.a = tc.a RIGHT JOIN ti ON ti.a = tc.a",
	"SELECT * FROM sbtest1 s1 JOIN sbtest1 s2 ON s1.id = s2.k",
	"SELECT * FROM t2 JOIN t2 ON t2.a = 1",
	"SELECT * FROM tp JOIN t2 ON tp.a = t2.a",
}

type sbJoinSpec struct {
	Kind        string  `json:"kind"`
	Flags       []uint  `json:"flags,omitempty"`
	LeftKeys    [][]int `json:"left_keys,omitempty"`
	RightKeys   [][]int `json:"right_keys,omitempty"`
	JoinType    int     `json:"join_type"`
	DeriveLeft  bool    `json:"derive_left,omitempty"`
	DeriveRight bool    `json:"derive_right,omitempty"`
	Left        uint    `json:"left,omitempty"`
	Right       uint    `json:"right,omitempty"`
}

type sbJoinOpCase struct {
	sql  string
	spec sbJoinSpec
}

var sbAllJoinTypes = []logicalop.JoinType{
	logicalop.InnerJoin, logicalop.LeftOuterJoin, logicalop.RightOuterJoin, logicalop.SemiJoin,
	logicalop.AntiSemiJoin, logicalop.LeftOuterSemiJoin, logicalop.AntiLeftOuterSemiJoin,
}

func sbJoinOpCases() []sbJoinOpCase {
	cs := []sbJoinOpCase{}
	preferAny := []uint{utilhint.PreferHashJoin, utilhint.PreferMergeJoin, utilhint.PreferINLJ,
		utilhint.PreferLeftAsINLJInner, utilhint.PreferRightAsHJBuild}
	for _, sql := range []string{
		"SELECT * FROM t2 JOIN tc",
		"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a",
		"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a AND t2.b <=> tc.b",
		"SELECT * FROM t3 RIGHT JOIN tc ON t3.x = tc.b AND t3.y = tc.a",
		"SELECT * FROM tc LEFT JOIN t3 ON tc.b = t3.x",
		"SELECT * FROM t2 JOIN tc USING (a)",
	} {
		cs = append(cs, sbJoinOpCase{sql, sbJoinSpec{Kind: "inspect", Flags: preferAny}})
	}
	// BuildKeyInfo: keys of the children given as column offsets.
	for _, sql := range []string{
		"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a",
		"SELECT * FROM t2 RIGHT JOIN tc ON t2.a = tc.a",
		"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a AND t2.b = tc.b",
		"SELECT * FROM t2 JOIN tc",
	} {
		for _, k := range []struct{ l, r [][]int }{
			{nil, nil}, {[][]int{{0}}, nil}, {nil, [][]int{{0}}}, {[][]int{{0}}, [][]int{{0}, {1, 0}}},
			{[][]int{{0, 1}}, [][]int{{0, 1}}}, {[][]int{{1}}, [][]int{{1}}},
		} {
			for _, jt := range []int{-1, int(logicalop.InnerJoin), int(logicalop.LeftOuterJoin), int(logicalop.RightOuterJoin), int(logicalop.SemiJoin)} {
				cs = append(cs, sbJoinOpCase{sql, sbJoinSpec{Kind: "keyinfo", LeftKeys: k.l, RightKeys: k.r, JoinType: jt}})
			}
		}
	}
	// AttachOnConds / ExtractOnCondition with each join type.
	for _, sql := range []string{
		"SELECT * FROM t2 JOIN tc ON t2.a = tc.a",
		"SELECT * FROM t2 JOIN tc ON t2.a = tc.a AND t2.c < tc.c AND tc.b = 'x' AND t2.d > 0",
		"SELECT * FROM t2 JOIN tc ON 1 AND t2.a = tc.a",
		"SELECT * FROM t2 JOIN tc ON 0",
		"SELECT * FROM t2 JOIN tc ON NULL AND t2.a <=> tc.a",
		"SELECT * FROM t2 JOIN tc ON t2.a + 1 = tc.a AND tc.a = 2",
		"SELECT * FROM t2 JOIN tc ON (t2.a = 1 AND tc.a = 1) OR (t2.a = 2 AND tc.a = 2)",
		"SELECT * FROM t2 JOIN tc ON (t2.a = tc.a AND t2.b = 'x') OR (t2.a = tc.c AND t2.b = 'y')",
		"SELECT * FROM t3 JOIN tc ON t3.x = tc.b",
		"SELECT * FROM t2, tc WHERE t2.a = tc.a AND tc.c IS NOT NULL",
	} {
		for _, jt := range sbAllJoinTypes {
			cs = append(cs, sbJoinOpCase{sql, sbJoinSpec{Kind: "attach", JoinType: int(jt)}})
		}
		for _, d := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			cs = append(cs, sbJoinOpCase{sql, sbJoinSpec{Kind: "extract", DeriveLeft: d[0], DeriveRight: d[1]}})
		}
	}
	// SetPreferredJoinType (containDifferentJoinTypes, setPreferredJoinTypeFromOneSide).
	sides := []uint{0, utilhint.PreferINLJ, utilhint.PreferINLHJ, utilhint.PreferINLMJ, utilhint.PreferHJBuild,
		utilhint.PreferHJProbe, utilhint.PreferHashJoin, utilhint.PreferMergeJoin, utilhint.PreferNoHashJoin,
		utilhint.PreferINLJ | utilhint.PreferNoIndexJoin, utilhint.PreferBCJoin, utilhint.PreferShuffleJoin,
		utilhint.PreferHJBuild | utilhint.PreferHashJoin, utilhint.PreferINLJ | utilhint.PreferINLHJ}
	for _, l := range sides {
		for _, r := range sides {
			cs = append(cs, sbJoinOpCase{"SELECT * FROM t2 JOIN tc", sbJoinSpec{Kind: "hints", Left: l, Right: r}})
		}
	}
	for _, sql := range []string{
		"SELECT * FROM t2 LEFT JOIN tc ON t2.a = tc.a AND t2.c < tc.c AND tc.b = 'x' AND t2.d > 0",
		"SELECT * FROM t2 JOIN tc USING (a)",
		"SELECT * FROM t2 JOIN tc",
	} {
		cs = append(cs, sbJoinOpCase{sql, sbJoinSpec{Kind: "replace"}})
	}
	return cs
}

func sbFindJoin(p base.LogicalPlan) (*logicalop.LogicalJoin, *logicalop.LogicalSelection) {
	if sel, ok := p.(*logicalop.LogicalSelection); ok {
		if j, ok := sel.Children()[0].(*logicalop.LogicalJoin); ok {
			return j, sel
		}
	}
	if j, ok := p.(*logicalop.LogicalJoin); ok {
		return j, nil
	}
	for _, c := range p.Children() {
		if j, sel := sbFindJoin(c); j != nil {
			return j, sel
		}
	}
	return nil, nil
}

func sbUIDs(cs []*expression.Column) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, c.UniqueID)
	}
	return out
}

func sbExecJoinOp(is infoschema.InfoSchema, sql string, spec sbJoinSpec) (any, error) {
	sctx := rbSession(rbDefaultOpts)
	vars := sctx.GetSessionVars()
	vars.SQLMode = mysql.ModeOnlyFullGroupBy
	stmt, err := parser.New().ParseOneStmt(sql, "", "")
	if err != nil {
		return nil, err
	}
	nodeW := resolve.NewNodeW(stmt)
	ret := &core.PreprocessorReturn{InfoSchema: is}
	if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
		return nil, err
	}
	builder, _ := core.NewPlanBuilder().Init(sctx.GetPlanCtx(), is, utilhint.NewQBHintHandler(nil))
	plan, err := builder.Build(context.Background(), nodeW)
	if err != nil {
		return nil, err
	}
	join, sel := sbFindJoin(plan.(base.LogicalPlan))
	if join == nil {
		return nil, fmt.Errorf("joinop: no LogicalJoin in %s", sql)
	}
	var conds []expression.Expression
	if sel != nil {
		conds = sel.Conditions
	}
	f := sbExprF(sctx)
	left, right := join.Children()[0], join.Children()[1]
	switch spec.Kind {
	case "inspect":
		types := []any{}
		schemas := []any{}
		for _, t := range sbAllJoinTypes {
			types = append(types, []any{t.String(), t.IsOuterJoin(), t.IsSemiJoin()})
			// For the left outer semi joins BuildLogicalJoinSchema appends the join's own last
			// column (the mark column of a real semi join; here the built join's last column).
			schemas = append(schemas, lrExprs(logicalop.BuildLogicalJoinSchema(t, join).Columns, f))
		}
		lk, rk, nullEQ, hasNullEQ := join.GetJoinKeys()
		nullEQs := []any{}
		for _, b := range nullEQ {
			nullEQs = append(nullEQs, b)
		}
		nlk, nrk := join.GetNAJoinKeys()
		prefer := []any{}
		for _, fl := range spec.Flags {
			prefer = append(prefer, join.PreferAny(fl))
		}
		pl, pr := join.GetPotentialPartitionKeys()
		lp := []any{}
		for _, k := range pl {
			lp = append(lp, []any{k.Col.UniqueID, k.CollateID})
		}
		rp := []any{}
		for _, k := range pr {
			rp = append(rp, []any{k.Col.UniqueID, k.CollateID})
		}
		return map[string]any{
			"types":      types,
			"keys":       []any{sbUIDs(lk), sbUIDs(rk), nullEQs, hasNullEQ},
			"na_keys":    []any{sbUIDs(nlk), sbUIDs(nrk)},
			"naaj":       join.IsNAAJ(),
			"prefer_any": prefer,
			"part_keys":  []any{lp, rp},
			"schemas":    schemas,
		}, nil
	case "keyinfo":
		setKeys := func(p base.LogicalPlan, ks [][]int) {
			keys := []expression.KeyInfo{}
			for _, k := range ks {
				key := expression.KeyInfo{}
				for _, i := range k {
					key = append(key, p.Schema().Columns[i])
				}
				keys = append(keys, key)
			}
			p.Schema().Keys = keys
		}
		setKeys(left, spec.LeftKeys)
		setKeys(right, spec.RightKeys)
		if spec.JoinType >= 0 {
			join.JoinType = logicalop.JoinType(spec.JoinType)
		}
		join.BuildKeyInfo(join.Schema(), []*expression.Schema{left.Schema(), right.Schema()})
		keys := []any{}
		for _, k := range join.Schema().Keys {
			keys = append(keys, sbUIDs(k))
		}
		return map[string]any{"keys": keys, "max_one_row": join.MaxOneRow()}, nil
	case "attach":
		join.JoinType = logicalop.JoinType(spec.JoinType)
		join.AttachOnConds(conds)
		return map[string]any{"plan": sbDump(sctx, join)}, nil
	case "extract":
		eq, l, r, o := join.ExtractOnCondition(conds, left.Schema(), right.Schema(), spec.DeriveLeft, spec.DeriveRight)
		return map[string]any{"eq": lrExprs(eq, f), "left": lrExprs(l, f), "right": lrExprs(r, f), "other": lrExprs(o, f)}, nil
	case "hints":
		join.LeftPreferJoinType = spec.Left
		join.RightPreferJoinType = spec.Right
		join.SetPreferredJoinType()
		warns := []any{}
		for _, w := range vars.StmtCtx.GetWarnings() {
			warns = append(warns, vec.GoString(w.Err.Error()))
		}
		return map[string]any{"prefer": join.PreferJoinType, "w": warns}, nil
	case "replace":
		oldCol := left.Schema().Columns[0]
		newCol := oldCol.Clone().(*expression.Column)
		newCol.UniqueID = 9999
		join.ReplaceExprColumns(map[string]*expression.Column{string(oldCol.HashCode()): newCol})
		return map[string]any{"plan": sbDump(sctx, join)}, nil
	}
	return nil, fmt.Errorf("joinop: unknown kind %s", spec.Kind)
}

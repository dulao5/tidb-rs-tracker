package domains

// Domain "sessionvars": block D05.2, SessionVars and the session hooks.
// T2 (this file so far): op `fresh` dumps the scalar fields of a fresh
// NewSessionVars(nil), in the order and with the exclusions of rsgen -sessionvars.
// The set / get / hook ops come with D05.2 T3-T5 (tidb-rs specs/D05.2-sessionvars).

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
)

const sessionvarsSeed = 5052

// sessionVarsExcluded mirrors cmd/rsgen/sessionvars.go (write-path scalar fields, X).
var sessionVarsExcluded = map[string]bool{
	"DDLReorgPriority": true, "DMLBatchSize": true, "RetryLimit": true, "DisableTxnAutoRetry": true,
	"ShardAllocateStep": true, "AllowAutoRandExplicitInsert": true, "AllowRemoveAutoInc": true,
	"BatchInsert": true, "BatchDelete": true, "BatchCommit": true, "BulkDMLEnabled": true,
}

func init() {
	exempt := map[string]string{}
	for _, n := range sessionvarsPending {
		exempt[n] = "pending: D05.2 T3-T7 (tidb-rs specs/D05.2-sessionvars/plan.md)"
	}
	vec.Register(&vec.Domain{
		Name:   "sessionvars",
		Seed:   sessionvarsSeed,
		Blocks: []string{"D05.2"},
		Coverage: map[string]vec.Coverage{
			"fresh": {Min: 1, GoFuncs: []string{"NewSessionVars", "NewUserVars", "NewPlanCacheParamList"}},
		},
		Exempt:   exempt,
		Gzip:     true,
		Generate: func(g *vec.Gen, _ *rand.Rand) { g.Add("sys/fresh", "fresh") },
		Exec:     sessionvarsExec,
	})
}

// scalarDump renders the scalar fields of sv like the Rust replay does: bools and
// integers in decimal, floats as their IEEE-754 bits, strings as is.
func scalarDump(sv *variable.SessionVars) map[string]string {
	out := map[string]string{}
	var walk func(v reflect.Value, t reflect.Type)
	walk = func(v reflect.Value, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			sf, fv := t.Field(i), v.Field(i)
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct && sf.Type.PkgPath() == t.PkgPath() {
				walk(fv, sf.Type)
				continue
			}
			if sessionVarsExcluded[sf.Name] {
				continue
			}
			switch sf.Type.Kind() {
			case reflect.Bool:
				out[sf.Name] = strconv.FormatBool(fv.Bool())
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				out[sf.Name] = strconv.FormatInt(fv.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				out[sf.Name] = strconv.FormatUint(fv.Uint(), 10)
			case reflect.Float64:
				out[sf.Name] = strconv.FormatUint(math.Float64bits(fv.Float()), 10)
			case reflect.Float32:
				out[sf.Name] = strconv.FormatUint(uint64(math.Float32bits(float32(fv.Float()))), 10)
			case reflect.String:
				out[sf.Name] = fv.String()
			}
		}
	}
	walk(reflect.ValueOf(sv).Elem(), reflect.TypeOf(*sv))
	return out
}

func sessionvarsExec(op string, _ []json.RawMessage) (any, error) {
	switch op {
	case "fresh":
		return scalarDump(variable.NewSessionVars(nil)), nil
	}
	panic(fmt.Sprintf("sessionvars: unknown op %q", op))
}

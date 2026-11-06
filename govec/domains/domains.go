// Package domains holds one file per vector domain. Each file registers a
// vec.Domain in init().
package domains

import (
	"github.com/pingcap/log"
	"go.uber.org/zap/zapcore"
)

// Quiet silences TiDB's global logger (e.g. terror.Log inside
// MyDecimal.String) so stderr stays readable. It does not affect results.
func Quiet() {
	log.SetLevel(zapcore.FatalLevel)
}

func init() { Quiet() }

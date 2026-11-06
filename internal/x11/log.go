package x11

import (
	"io"

	"github.com/jezek/xgb"
)

func init() { xgb.Logger.SetOutput(io.Discard) }

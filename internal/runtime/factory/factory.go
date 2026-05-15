package factory

import (
	"fmt"

	"github.com/sdougbrown/avenor/internal/runtime"
	"github.com/sdougbrown/avenor/internal/runtime/opencodeacp"
	"github.com/sdougbrown/avenor/internal/runtime/opencodehttp"
)

func NewProvider(startOpts runtime.StartOptions, backend string) (runtime.Provider, error) {
	switch backend {
	case "opencode-acp":
		return opencodeacp.NewWithOptions(startOpts), nil
	case "opencode-http":
		return opencodehttp.NewWithOptions(startOpts)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
}

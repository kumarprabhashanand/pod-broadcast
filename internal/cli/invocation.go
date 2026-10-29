package cli

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// invocation is one parsed broadcast command: a target plus either a raw request or a preset.
type invocation struct {
	target target.Ref
	method string
	path   string
	preset presetCall
}

type presetCall struct {
	group string
	name  string
	args  []string
}

func parseInvocation(args []string, selector, method string) (invocation, error) {
	var inv invocation
	switch {
	case selector != "":
		if len(args) > 0 && isTargetArg(args[0]) {
			return inv, fmt.Errorf("use either TARGET (%s) or --selector, not both", args[0])
		}
		sel, err := labels.Parse(selector)
		if err != nil {
			return inv, fmt.Errorf("invalid selector %q: %w", selector, err)
		}
		inv.target = target.Ref{Selector: sel}
	case len(args) == 0:
		return inv, errors.New("missing TARGET, for example svc/orders, or --selector")
	case !isTargetArg(args[0]):
		return inv, fmt.Errorf("unknown command %q: targets look like svc/NAME or deploy/NAME", args[0])
	default:
		ref, err := target.ParseRef(args[0])
		if err != nil {
			return inv, err
		}
		inv.target = ref
		args = args[1:]
	}

	if method != "" {
		if len(args) != 1 {
			return inv, fmt.Errorf("-X %s needs exactly one PATH argument, got %d", method, len(args))
		}
		if !strings.HasPrefix(args[0], "/") {
			return inv, fmt.Errorf("PATH %q must start with /", args[0])
		}
		inv.method, inv.path = method, args[0]
		return inv, nil
	}
	if len(args) < 2 {
		return inv, errors.New("missing operation: give -X METHOD PATH, or a preset as GROUP PRESET")
	}
	inv.preset = presetCall{group: args[0], name: args[1], args: args[2:]}
	return inv, nil
}

// isTargetArg tells a KIND/NAME target apart from a command name and from a PATH.
func isTargetArg(s string) bool {
	return strings.Contains(s, "/") && !strings.HasPrefix(s, "/")
}

package preset

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
)

const DefaultBasePath = "/actuator"

// Options holds the preset flags.
type Options struct {
	Cache    string
	BasePath string
}

// Preset is a named, built-in request template.
type Preset struct {
	Group   string
	Name    string
	Args    []string
	Flags   string
	Request string
	Port    string
	build   buildFunc
	// revert builds the request that undoes this one, for --for.
	revert buildFunc
}

type buildFunc func(args []string, o Options) (method, path string, body []byte, err error)

// Call is a preset turned into a request. Port is the port name to use when neither --port nor the
// pod's port annotation names one. Revert is set when the preset can be undone.
type Call struct {
	Spec   request.Spec
	Revert *request.Spec
	Port   string
}

var levels = []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL", "OFF"}

var all = []Preset{
	{
		Group: "spring", Name: "evict-caches", Flags: "[--cache NAME]",
		Request: "DELETE {base}/caches[/NAME]", Port: "management",
		build: func(_ []string, o Options) (string, string, []byte, error) {
			if o.Cache != "" {
				return http.MethodDelete, "/caches/" + url.PathEscape(o.Cache), nil, nil
			}
			return http.MethodDelete, "/caches", nil, nil
		},
	},
	{
		Group: "spring", Name: "set-log-level", Args: []string{"LOGGER", "LEVEL"},
		Request: `POST {base}/loggers/LOGGER {"configuredLevel":"LEVEL"}`, Port: "management",
		build: func(args []string, _ Options) (string, string, []byte, error) {
			logger, err := loggerName(args[0])
			if err != nil {
				return "", "", nil, err
			}
			level := strings.ToUpper(args[1])
			if !slices.Contains(levels, level) {
				return "", "", nil, fmt.Errorf("invalid log level %q: use %s", args[1], strings.Join(levels, ", "))
			}
			return http.MethodPost, "/loggers/" + logger, []byte(`{"configuredLevel":"` + level + `"}`), nil
		},
		revert: func(args []string, o Options) (string, string, []byte, error) { return resetLogLevel(args[:1], o) },
	},
	{
		Group: "spring", Name: "reset-log-level", Args: []string{"LOGGER"},
		Request: "POST {base}/loggers/LOGGER {}", Port: "management", build: resetLogLevel,
	},
	{
		Group: "spring", Name: "refresh", Request: "POST {base}/refresh", Port: "management",
		build: func([]string, Options) (string, string, []byte, error) { return http.MethodPost, "/refresh", nil, nil },
	},
	{
		Group: "spring", Name: "health", Request: "GET {base}/health", Port: "management",
		build: func([]string, Options) (string, string, []byte, error) { return http.MethodGet, "/health", nil, nil },
	},
	{
		Group: "spring", Name: "info", Request: "GET {base}/info", Port: "management",
		build: func([]string, Options) (string, string, []byte, error) { return http.MethodGet, "/info", nil, nil },
	},
}

func All() []Preset { return slices.Clone(all) }

func Find(group, name string) (Preset, error) {
	for _, p := range all {
		if p.Group == group && p.Name == name {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("unknown preset %q: run kubectl broadcast presets to list them", group+" "+name)
}

// Usage is the preset's command line, e.g. "spring set-log-level LOGGER LEVEL".
func (p Preset) Usage() string {
	return strings.Join(slices.DeleteFunc([]string{p.Group, p.Name, strings.Join(p.Args, " "), p.Flags}, func(s string) bool { return s == "" }), " ")
}

func (p Preset) Build(args []string, o Options) (Call, error) {
	if len(args) != len(p.Args) {
		return Call{}, fmt.Errorf("usage: kubectl broadcast TARGET %s", p.Usage())
	}
	if o.Cache != "" && p.Flags == "" {
		return Call{}, fmt.Errorf("--cache only applies to spring evict-caches")
	}
	base := DefaultBasePath
	if o.BasePath != "" {
		var err error
		if base, err = normalizeBasePath(o.BasePath); err != nil {
			return Call{}, fmt.Errorf("--base-path: %w", err)
		}
	}
	spec, err := newSpec(p.build, args, o, base)
	if err != nil {
		return Call{}, err
	}
	call := Call{Spec: spec, Port: p.Port}
	if p.revert != nil {
		revert, err := newSpec(p.revert, args, o, base)
		if err != nil {
			return Call{}, err
		}
		call.Revert = &revert
	}
	return call, nil
}

func newSpec(build buildFunc, args []string, o Options, base string) (request.Spec, error) {
	method, path, body, err := build(args, o)
	if err != nil {
		return request.Spec{}, err
	}
	spec := request.Spec{Method: method, Path: base + path, Body: body}
	if body != nil {
		spec.Headers = []request.Header{{Name: "Content-Type", Value: "application/json"}}
	}
	return spec, nil
}

func resetLogLevel(args []string, _ Options) (string, string, []byte, error) {
	logger, err := loggerName(args[0])
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, "/loggers/" + logger, []byte(`{}`), nil
}

func normalizeBasePath(s string) (string, error) {
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("base path %q must start with /", s)
	}
	if strings.ContainsAny(s, "?# ") {
		return "", fmt.Errorf("base path %q can't contain '?', '#' or spaces", s)
	}
	return strings.TrimRight(s, "/"), nil
}

func loggerName(s string) (string, error) {
	if s == "" || strings.ContainsAny(s, "/ ?#") {
		return "", errors.New("logger name must be non-empty, without '/', '?', '#' or spaces, e.g. com.shop.orders or ROOT")
	}
	return url.PathEscape(s), nil
}

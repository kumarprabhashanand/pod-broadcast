package output

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const maxTableBody = 60

func writeTable(w io.Writer, r Report) error {
	if r.DryRun {
		return writeDryRun(w, r)
	}
	showBody := slices.ContainsFunc(r.Pods, func(p Pod) bool { return strings.TrimSpace(p.Body) != "" })
	header := "POD\tSTATE\tCODE\tTIME\tATTEMPTS\tERROR"
	if showBody {
		header += "\tBODY"
	}
	rows := []string{header}
	for _, p := range r.Pods {
		code := ""
		if p.Code != 0 {
			code = strconv.Itoa(p.Code)
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s", p.Name, p.State, code, duration(p.DurationMs), p.Attempts, p.Error)
		if showBody {
			row += "\t" + oneLine(p.Body, maxTableBody)
		}
		rows = append(rows, row)
	}
	if err := WriteRows(w, rows); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, summary(r.Summary))
	return err
}

func writeDryRun(w io.Writer, r Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", r.Request.Method, r.Request.Path)
	names := make([]string, 0, len(r.Request.Headers))
	for name := range r.Request.Headers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&b, "%s: %s\n", name, r.Request.Headers[name])
	}
	if r.Request.Body != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Request.Body)
	}
	b.WriteString("\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}

	rows := []string{"POD\tIP\tPORT\tSCHEME\tERROR"}
	for _, p := range r.Pods {
		port := ""
		if p.Port != 0 {
			port = strconv.Itoa(int(p.Port))
		}
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%s\t%s", p.Name, p.IP, port, p.Scheme, p.Error))
	}
	if err := WriteRows(w, rows); err != nil {
		return err
	}
	callable := r.Summary.Targeted - r.Summary.Failed
	_, err := fmt.Fprintf(w, "dry run: %d of %d pods would be called; nothing was sent\n", callable, r.Summary.Targeted)
	return err
}

// WriteRows aligns tab-separated rows into columns, without trailing spaces.
func WriteRows(w io.Writer, rows []string) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 8, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintln(tw, row)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		out.WriteString(strings.TrimRight(line, " "))
		out.WriteString("\n")
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func summary(s Summary) string {
	parts := []string{fmt.Sprintf("%d succeeded", s.Succeeded)}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Gone > 0 {
		parts = append(parts, fmt.Sprintf("%d gone", s.Gone))
	}
	if s.Reverted > 0 {
		parts = append(parts, fmt.Sprintf("%d reverted", s.Reverted))
	}
	if s.RevertFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d revert failed", s.RevertFailed))
	}
	return fmt.Sprintf("%d targeted: %s", s.Targeted, strings.Join(parts, ", "))
}

func duration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return s
}

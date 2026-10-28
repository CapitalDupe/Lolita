package ui

import (
        "bytes"
        "fmt"
        "io"
        "log"
        "regexp"
        "strings"
)

// The fyne toolkit reports non-fatal problems through the standard
// library log (fyne.LogError). Some of that output is expected and
// harmless on the systems sxops targets - e.g. the file dialog probing
// the standard user folders on installs without a complete
// user-dirs.dirs. The helpers here capture the log while such code runs
// and re-print everything EXCEPT the expected blocks, so real errors
// still reach the operator. sxops itself never uses the std log, so the
// capture windows only ever hold fyne output.

// withLogCaptured redirects the standard library log into a buffer
// while fn runs and restores the previous writer afterwards. The log
// package serializes writes under a mutex, so lines never interleave;
// anything a concurrent goroutine logs after the restore simply goes
// to the original writer again. fn must be synchronous.
func withLogCaptured(fn func()) string {
        var buf bytes.Buffer
        prev := log.Writer()
        log.SetOutput(&buf)
        fn()
        log.SetOutput(prev)
        return buf.String()
}

// reprintExceptBlocks writes every captured log record to out except
// the blocks whose header line makes dropHeader true. A block is a
// header line plus the two-space-indented "  Cause: ..." / "  At: ..."
// continuation lines fyne.LogError prints after it; anything else - a
// real error, or a line some other goroutine logged during the window -
// survives verbatim.
func reprintExceptBlocks(captured string, out io.Writer, dropHeader func(string) bool) {
        skip := false
        for _, line := range strings.Split(captured, "\n") {
                if strings.TrimSpace(line) == "" {
                        continue // trailing newline of the last log call
                }
                if skip && isLogContinuation(line) {
                        continue // continuation of the dropped block
                }
                skip = false
                if dropHeader(line) {
                        skip = true
                        continue
                }
                fmt.Fprintln(out, line)
        }
}

// isLogContinuation reports whether a captured log line is a
// continuation of the previous log record - the two-space-indented
// "  Cause: ..." / "  At: ..." lines fyne.LogError prints after a
// header. The std log flags prepend a date or date+time prefix to
// every line ("2006/01/02 " / "2006/01/02 15:04:05 "), so the indent
// has to be looked at behind that prefix; with no timestamp at all
// the line must simply start with the indent.
var logTimePrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}( \d{2}:\d{2}(:\d{2}(\.\d+)?)?)? `)

func isLogContinuation(line string) bool {
        msg := line
        if loc := logTimePrefix.FindStringIndex(msg); loc != nil {
                msg = msg[loc[1]:] // strip the date / date-time prefix
        }
        return strings.HasPrefix(msg, "  ")
}

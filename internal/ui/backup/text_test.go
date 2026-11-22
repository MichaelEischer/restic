package backup

import (
	"strconv"
	"testing"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/test"
	"github.com/restic/restic/internal/ui"
)

func createTextProgress() (*ui.MockTerminal, ProgressPrinter) {
	term := &ui.MockTerminal{}
	printer := NewTextProgress(term, 3)
	return term, printer
}

func TestError(t *testing.T) {
	term, printer := createTextProgress()
	test.Equals(t, printer.Error("/path", errors.New("error \"message\"")), nil)
	test.Equals(t, []string{"error: error \"message\"\n"}, term.Errors)
}

func TestScannerError(t *testing.T) {
	term, printer := createTextProgress()
	test.Equals(t, printer.ScannerError("/path", errors.New("error \"message\"")), nil)
	test.Equals(t, []string{"scan: error \"message\"\n"}, term.Errors)
}

func TestErrorQuotesControlCharacters(t *testing.T) {
	term, printer := createTextProgress()
	err := errors.New("open " + "a\x1b[2Jb" + ": permission denied")
	test.Equals(t, printer.Error("a\x1b[2Jb", err), nil)
	test.Equals(t, []string{"error: " + strconv.Quote(err.Error()) + "\n"}, term.Errors)

	printer.ExcludedItem("dir\nfile")
	test.Equals(t, []string{"excluded " + strconv.Quote("dir\nfile")}, term.Output)
}

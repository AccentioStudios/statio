package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

var (
	cTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	cOK    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	cWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	cErr   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	cFaint = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	cCode  = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	cBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(0, 1)
)

// interactive reports whether we can run a guided wizard (stdin is a real terminal).
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func banner(title, subtitle string) {
	fmt.Println()
	fmt.Println(cBox.Render(cTitle.Render(title) + "\n" + cFaint.Render(subtitle)))
	fmt.Println()
}

func okLine(format string, a ...any)   { fmt.Println(cOK.Render("✓ ") + fmt.Sprintf(format, a...)) }
func warnLine(format string, a ...any) { fmt.Println(cWarn.Render("! ") + fmt.Sprintf(format, a...)) }
func failLine(format string, a ...any) { fmt.Println(cErr.Render("✗ ") + fmt.Sprintf(format, a...)) }
func info(format string, a ...any)     { fmt.Println(cFaint.Render(fmt.Sprintf(format, a...))) }

func sectionTitle(s string) {
	fmt.Println()
	fmt.Println(cTitle.Render(s))
}

// codeBlock prints commands the user should run, highlighted.
func codeBlock(lines ...string) {
	for _, l := range lines {
		fmt.Println("  " + cCode.Render(l))
	}
}

// errLine formats a red error string (for returned errors shown by the root).
func errLine(format string, a ...any) string {
	return cErr.Render(fmt.Sprintf(format, a...))
}

func trimmed(s string) string { return strings.TrimSpace(s) }

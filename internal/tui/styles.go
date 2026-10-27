package tui

import "github.com/charmbracelet/lipgloss"

// Palette: cool neutrals with one warm accent so Health/State badges pop.
var (
	cAccent  = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#5EEAD4"} // teal
	cKey     = lipgloss.AdaptiveColor{Light: "#4C1D95", Dark: "#C4B5FD"} // lavender
	cSection = lipgloss.AdaptiveColor{Light: "#1E3A8A", Dark: "#93C5FD"} // blue
	cString  = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E5E7EB"}
	cNumber  = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"} // amber
	cBool    = lipgloss.AdaptiveColor{Light: "#9D174D", Dark: "#F9A8D4"} // pink
	cLink    = lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#7DD3FC"} // sky
	cDim     = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#6B7280"}
	cFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#4B5563"}
	cOK      = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	cWarn    = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	cCrit    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	cBarBG   = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#1F2937"}
	cBarFG   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F3F4F6"}
	cSelBG   = lipgloss.AdaptiveColor{Light: "#CCFBF1", Dark: "#134E4A"}
	cHiBG    = lipgloss.AdaptiveColor{Light: "#FEF08A", Dark: "#854D0E"}
	cBorder  = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}
	cBorderF = cAccent
)

var (
	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sBar      = lipgloss.NewStyle().Background(cBarBG).Foreground(cBarFG)
	sBarKey   = lipgloss.NewStyle().Background(cBarBG).Foreground(cAccent).Bold(true)
	sBarDim   = lipgloss.NewStyle().Background(cBarBG).Foreground(cDim)
	sCrumb    = lipgloss.NewStyle().Foreground(cLink)
	sCrumbSep = lipgloss.NewStyle().Foreground(cFaint)
	sCrumbEnd = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	sKey     = lipgloss.NewStyle().Foreground(cKey)
	sKeyMeta = lipgloss.NewStyle().Foreground(cFaint)
	sSection = lipgloss.NewStyle().Foreground(cSection).Bold(true)
	sString  = lipgloss.NewStyle().Foreground(cString)
	sNumber  = lipgloss.NewStyle().Foreground(cNumber)
	sBool    = lipgloss.NewStyle().Foreground(cBool)
	sNull    = lipgloss.NewStyle().Foreground(cFaint).Italic(true)
	sLink    = lipgloss.NewStyle().Foreground(cLink).Underline(true)
	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sFaint   = lipgloss.NewStyle().Foreground(cFaint)
	sPunct   = lipgloss.NewStyle().Foreground(cFaint)
	sOK      = lipgloss.NewStyle().Foreground(cOK).Bold(true)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	sCrit    = lipgloss.NewStyle().Foreground(cCrit).Bold(true)
	sErr     = lipgloss.NewStyle().Foreground(cCrit)
	sHi      = lipgloss.NewStyle().Background(cHiBG)

	sSel    = lipgloss.NewStyle().Background(cSelBG).Bold(true)
	sSelDim = lipgloss.NewStyle().Background(cSelBG).Foreground(cDim)

	sPane       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder)
	sPaneFocus  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorderF)
	sPaneTitle  = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	sPaneTitleF = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(1, 3)
	sLabel    = lipgloss.NewStyle().Foreground(cDim).Width(10)
	sLabelF   = lipgloss.NewStyle().Foreground(cAccent).Bold(true).Width(10)
	sHelpKey  = lipgloss.NewStyle().Foreground(cAccent).Bold(true).Width(14)
	sHelpText = lipgloss.NewStyle().Foreground(cString)
)

// enumStyle colors well-known Redfish enum values regardless of key.
func enumStyle(s string) (lipgloss.Style, bool) {
	switch s {
	case "OK", "Enabled", "On", "Healthy", "Completed", "Normal", "Up", "LinkUp", "Ready", "Online", "Present", "Good":
		return sOK, true
	case "Warning", "StandbyOffline", "StandbySpare", "InTest", "Starting", "Updating", "Degraded", "Quiesced", "Pending", "Running", "Charging", "PoweringOn", "PoweringOff", "Deferring":
		return sWarn, true
	case "Critical", "Off", "Disabled", "Absent", "UnavailableOffline", "Failed", "Exception", "Fault", "Down", "LinkDown", "Killed", "Cancelled", "Error", "Unknown":
		return sCrit, true
	}
	return lipgloss.Style{}, false
}

// healthIcon returns a glyph for a Health value.
func healthIcon(h string) string {
	switch h {
	case "OK":
		return sOK.Render("●")
	case "Warning":
		return sWarn.Render("●")
	case "Critical":
		return sCrit.Render("●")
	}
	return sFaint.Render("○")
}

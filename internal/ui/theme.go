package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// readableTheme wraps the app theme with one change: the "disabled"
// foreground color renders at full contrast instead of the default dim
// grey. sxops shows server addresses, onion hosts and log tails in
// read-only (disabled) entries - with the stock color those long
// monospace texts are barely legible, particularly in dark mode.
//
// Everything else (colors, fonts, icons, sizes) delegates to the wrapped
// theme, so the system light/dark preference keeps working: Fyne passes
// the active variant into Color().
type readableTheme struct{ base fyne.Theme }

// installReadableTheme wraps the current app theme once at startup.
func installReadableTheme(a fyne.App) {
	cur := a.Settings().Theme()
	if cur == nil {
		cur = theme.DefaultTheme()
	}
	a.Settings().SetTheme(readableTheme{base: cur})
}

func (t readableTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameDisabled {
		// full-contrast text for read-only fields
		return t.base.Color(theme.ColorNameForeground, v)
	}
	return t.base.Color(name, v)
}

func (t readableTheme) Font(style fyne.TextStyle) fyne.Resource { return t.base.Font(style) }

func (t readableTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }

func (t readableTheme) Size(name fyne.ThemeSizeName) float32 { return t.base.Size(name) }

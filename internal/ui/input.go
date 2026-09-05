package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// forwardKey translates a Bubble Tea key press into what the shell should
// receive. Printable text is sent verbatim; everything else becomes a key
// event that the session encodes according to the terminal's current mode.
func forwardKey(s workspace.Session, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		text := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			text = " "
		}
		if msg.Alt {
			text = "\x1b" + text
		}
		s.SendText(text)
	default:
		if k, ok := keyEvent(msg.Type); ok {
			if msg.Alt {
				k.Mod |= uv.ModAlt
			}
			s.SendKey(k)
		}
	}
}

// specialKeys maps Bubble Tea key types with a direct counterpart.
var specialKeys = map[tea.KeyType]uv.KeyPressEvent{
	tea.KeyEnter:     {Code: uv.KeyEnter},
	tea.KeyTab:       {Code: uv.KeyTab},
	tea.KeyShiftTab:  {Code: uv.KeyTab, Mod: uv.ModShift},
	tea.KeyBackspace: {Code: uv.KeyBackspace},
	tea.KeyEsc:       {Code: uv.KeyEscape},
	tea.KeyUp:        {Code: uv.KeyUp},
	tea.KeyDown:      {Code: uv.KeyDown},
	tea.KeyLeft:      {Code: uv.KeyLeft},
	tea.KeyRight:     {Code: uv.KeyRight},
	tea.KeyHome:      {Code: uv.KeyHome},
	tea.KeyEnd:       {Code: uv.KeyEnd},
	tea.KeyPgUp:      {Code: uv.KeyPgUp},
	tea.KeyPgDown:    {Code: uv.KeyPgDown},
	tea.KeyDelete:    {Code: uv.KeyDelete},
	tea.KeyInsert:    {Code: uv.KeyInsert},

	tea.KeyCtrlUp:     {Code: uv.KeyUp, Mod: uv.ModCtrl},
	tea.KeyCtrlDown:   {Code: uv.KeyDown, Mod: uv.ModCtrl},
	tea.KeyCtrlLeft:   {Code: uv.KeyLeft, Mod: uv.ModCtrl},
	tea.KeyCtrlRight:  {Code: uv.KeyRight, Mod: uv.ModCtrl},
	tea.KeyShiftUp:    {Code: uv.KeyUp, Mod: uv.ModShift},
	tea.KeyShiftDown:  {Code: uv.KeyDown, Mod: uv.ModShift},
	tea.KeyShiftLeft:  {Code: uv.KeyLeft, Mod: uv.ModShift},
	tea.KeyShiftRight: {Code: uv.KeyRight, Mod: uv.ModShift},

	tea.KeyCtrlAt:           {Code: uv.KeySpace, Mod: uv.ModCtrl},
	tea.KeyCtrlBackslash:    {Code: '\\', Mod: uv.ModCtrl},
	tea.KeyCtrlCloseBracket: {Code: ']', Mod: uv.ModCtrl},
	tea.KeyCtrlCaret:        {Code: '^', Mod: uv.ModCtrl},
	tea.KeyCtrlUnderscore:   {Code: '_', Mod: uv.ModCtrl},
}

func keyEvent(t tea.KeyType) (uv.KeyPressEvent, bool) {
	if k, ok := specialKeys[t]; ok {
		return k, true
	}
	// ctrl+a..ctrl+z are the control bytes 1..26. Tab (9), enter (13) and
	// esc (27) were already matched above, so they keep their own meaning.
	if t >= tea.KeyCtrlA && t <= tea.KeyCtrlZ {
		return uv.KeyPressEvent{Code: 'a' + rune(t-tea.KeyCtrlA), Mod: uv.ModCtrl}, true
	}
	// Function keys count downwards in Bubble Tea and upwards in uv.
	if t <= tea.KeyF1 && t >= tea.KeyF12 {
		return uv.KeyPressEvent{Code: uv.KeyF1 + rune(tea.KeyF1-t)}, true
	}
	return uv.KeyPressEvent{}, false
}

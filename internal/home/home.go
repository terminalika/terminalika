// Package home is the launcher's landing screen: a full-screen animated
// hero with nothing else on it, a Raycast-style type-to-launch overlay,
// and - on [↓] - the retro game library sliding in under the hero.
//
// The screen also shows what the agent hub is doing (which agents are
// listened to, the last event) and surfaces live agent events as a toast,
// but never owns them: events arrive on a channel the hub fans out to.
package home

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/terminalika/terminalika-core/highscore"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/keystate"
	"github.com/terminalika/terminalika/internal/ui"
)

// Status is what the home screen shows about the hub; it's re-read every
// frame so changes (the listener seat moving away, say) show immediately.
type Status struct {
	Agents    []agents.Agent
	Notify    string
	AutoPause bool
	Listening bool
	Webhook   string
}

// Prompt is the hero-mode bottom bar text.
const Prompt = "Press [↓] Down Arrow to explore games or type a game name directly (e.g., 'snake') and press Enter."

// shortPrompt is used when the terminal is too narrow for Prompt.
const shortPrompt = "[↓] explore games · type a name + Enter to launch"

const (
	framePeriod = 40 * time.Millisecond // 25 fps
	toastFor    = 8 * time.Second
	cardW       = previewCols + 4
	cardH       = previewRows + 4
	titleWord   = "TERMINALIKA"
	tagline     = "AI focus hub · notification listener · retro game library"
)

type mode int

const (
	modeHero mode = iota
	modeExplore
	modeSearch
)

// particle is one speck of the hero's drifting background.
type particle struct {
	x, y  float64
	vy    float64
	depth int
}

type toast struct {
	ev    agents.Event
	until time.Time
}

// Feed is where the home screen gets agent events from: the hub. Current
// is the one event the player hasn't been shown yet, if any - polled every
// frame rather than received on a channel, so an event the player already
// met inside a game (and marked seen there) never resurfaces here as a
// toast. Latest is for the passive "last event" line.
type Feed interface {
	Current() (agents.Event, bool)
	MarkSeen(agents.Event)
	Latest() (agents.Event, bool)
}

// Home is the landing screen.
type Home struct {
	screen tcell.Screen
	games  []string
	feed   Feed
	status func() Status
	scores map[string]int

	mode      mode
	prevMode  mode
	heroShift float64 // 0 = hero centered (full screen), 1 = hero parked at the top
	frame     int
	now       func() time.Time
	rng       *rand.Rand

	particles []particle
	pw, ph    int

	// snake underline animation
	snakeHead, snakeLen, food int

	// explore
	sel int
	lay layout

	// search
	query   string
	matches []match
	msel    int

	toast *toast
}

// New creates the home screen. feed may be nil when no agent hub runs.
func New(screen tcell.Screen, games []string, feed Feed, status func() Status) *Home {
	if status == nil {
		status = func() Status { return Status{} }
	}
	return &Home{
		screen:   screen,
		games:    games,
		feed:     feed,
		status:   status,
		now:      time.Now,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		snakeLen: 4,
		food:     -1,
	}
}

// Run drives the screen until the player picks a game (name, true) or
// quits ("", false). It can be called again after a game ends.
func (h *Home) Run() (string, bool) {
	h.loadScores()
	h.mode = modeHero
	h.heroShift = 0
	h.query = ""
	h.draw()

	ticker := time.NewTicker(framePeriod)
	defer ticker.Stop()
	for {
		for h.screen.HasPendingEvent() {
			ev := h.screen.PollEvent()
			if ev == nil {
				return "", false
			}
			switch ev := ev.(type) {
			case *tcell.EventResize:
				h.screen.Sync()
				h.particles = nil
			case *tcell.EventKey:
				if keystate.IsRelease(ev) {
					continue
				}
				if name, done := h.handleKey(ev); done {
					return name, name != ""
				}
			}
		}
		h.pollHub()
		h.step()
		h.draw()
		<-ticker.C
	}
}

func (h *Home) loadScores() {
	h.scores = map[string]int{}
	store, err := highscore.Open(highscore.DefaultPath())
	if err != nil {
		return
	}
	for _, g := range h.games {
		h.scores[g] = store.Best(g)
	}
}

// pollHub adopts the hub's current unseen event, if any, as the toast, and
// marks it seen right away: from here on nothing shows it again, not even
// this screen after the toast is dismissed or times out.
func (h *Home) pollHub() {
	if h.feed == nil {
		return
	}
	ev, ok := h.feed.Current()
	if !ok {
		return
	}
	h.feed.MarkSeen(ev)
	h.toast = &toast{ev: ev, until: h.now().Add(toastFor)}
}

// handleKey returns (game, true) to launch, ("", true) to quit, or
// (_, false) to keep going.
func (h *Home) handleKey(ev *tcell.EventKey) (string, bool) {
	if ev.Key() == tcell.KeyCtrlC {
		return "", true
	}
	// A toast is dismissed by any key, which is then handled normally.
	h.toast = nil

	switch h.mode {
	case modeSearch:
		return h.handleSearchKey(ev)
	case modeExplore:
		return h.handleExploreKey(ev)
	default:
		return h.handleHeroKey(ev)
	}
}

func (h *Home) handleHeroKey(ev *tcell.EventKey) (string, bool) {
	switch ev.Key() {
	case tcell.KeyEscape:
		return "", true
	case tcell.KeyDown, tcell.KeyEnter:
		h.setMode(modeExplore)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			return "", true
		case 'j':
			h.setMode(modeExplore)
		default:
			h.startSearch(ev.Rune())
		}
	}
	return "", false
}

func (h *Home) handleExploreKey(ev *tcell.EventKey) (string, bool) {
	cols := h.gridCols()
	switch ev.Key() {
	case tcell.KeyEscape:
		h.setMode(modeHero)
	case tcell.KeyEnter:
		if len(h.games) > 0 {
			return h.games[h.sel], true
		}
	case tcell.KeyUp:
		if h.sel-cols < 0 {
			h.setMode(modeHero)
		} else {
			h.sel -= cols
		}
	case tcell.KeyDown:
		if h.sel+cols < len(h.games) {
			h.sel += cols
		}
	case tcell.KeyLeft:
		if h.sel > 0 {
			h.sel--
		}
	case tcell.KeyRight:
		if h.sel+1 < len(h.games) {
			h.sel++
		}
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k':
			if h.sel-cols < 0 {
				h.setMode(modeHero)
			} else {
				h.sel -= cols
			}
		case 'j':
			if h.sel+cols < len(h.games) {
				h.sel += cols
			}
		case 'h':
			if h.sel > 0 {
				h.sel--
			}
		case 'l':
			if h.sel+1 < len(h.games) {
				h.sel++
			}
		case 'q':
			return "", true
		default:
			h.startSearch(ev.Rune())
		}
	}
	return "", false
}

func (h *Home) handleSearchKey(ev *tcell.EventKey) (string, bool) {
	switch ev.Key() {
	case tcell.KeyEscape:
		h.endSearch()
	case tcell.KeyEnter:
		if len(h.matches) > 0 {
			return h.matches[h.msel].name, true
		}
	case tcell.KeyUp:
		if h.msel > 0 {
			h.msel--
		}
	case tcell.KeyDown:
		if h.msel+1 < len(h.matches) {
			h.msel++
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		r := []rune(h.query)
		if len(r) <= 1 {
			h.endSearch()
		} else {
			h.query = string(r[:len(r)-1])
			h.refreshMatches()
		}
	case tcell.KeyRune:
		if len([]rune(h.query)) < 32 {
			h.query += string(ev.Rune())
			h.refreshMatches()
		}
	}
	return "", false
}

func (h *Home) startSearch(r rune) {
	if r == ' ' {
		return
	}
	h.prevMode = h.mode
	h.mode = modeSearch
	h.query = string(r)
	h.msel = 0
	h.refreshMatches()
}

func (h *Home) endSearch() {
	h.query = ""
	h.matches = nil
	h.mode = h.prevMode
}

func (h *Home) refreshMatches() {
	h.matches = fuzzySearch(h.query, h.games)
	if h.msel >= len(h.matches) {
		h.msel = 0
	}
}

func (h *Home) setMode(m mode) {
	h.mode = m
	if m == modeExplore && h.sel >= len(h.games) {
		h.sel = 0
	}
}

// heroTarget is where the hero wants to be: parked at the top whenever the
// library is (or is about to be) visible.
func (h *Home) heroTarget() float64 {
	if h.mode == modeExplore || (h.mode == modeSearch && h.prevMode == modeExplore) {
		return 1
	}
	return 0
}

// step advances the animations by one frame.
func (h *Home) step() {
	h.frame++

	// Ease the hero toward its target: a quarter of the remaining distance
	// per frame reads as a quick, smooth slide (~300 ms).
	target := h.heroTarget()
	h.heroShift += (target - h.heroShift) * 0.25
	if math.Abs(target-h.heroShift) < 0.005 {
		h.heroShift = target
	}

	w, hh := h.screen.Size()
	if h.particles == nil || h.pw != w || h.ph != hh {
		h.seedParticles(w, hh)
	}
	for i := range h.particles {
		p := &h.particles[i]
		p.y -= p.vy
		if p.y < 0 {
			p.y = float64(hh)
			p.x = h.rng.Float64() * float64(w)
		}
	}

	if h.frame%3 == 0 {
		h.stepSnake()
	}
	if h.toast != nil && h.now().After(h.toast.until) {
		h.toast = nil
	}
}

func (h *Home) seedParticles(w, hh int) {
	h.pw, h.ph = w, hh
	n := w * hh / 60
	if n < 8 {
		n = 8
	}
	h.particles = make([]particle, n)
	for i := range h.particles {
		h.particles[i] = particle{
			x:     h.rng.Float64() * float64(w),
			y:     h.rng.Float64() * float64(hh),
			vy:    0.03 + h.rng.Float64()*0.12,
			depth: h.rng.Intn(3),
		}
	}
}

// stepSnake moves the little snake along the underline; eating the food
// grows it, until it's long enough to shed back to its starting length.
func (h *Home) stepSnake() {
	width := h.underlineWidth()
	if width <= 0 {
		return
	}
	if h.food < 0 || h.food >= width {
		h.food = h.rng.Intn(width)
	}
	h.snakeHead = (h.snakeHead + 1) % width
	if h.snakeHead == h.food {
		h.snakeLen++
		if h.snakeLen > 12 {
			h.snakeLen = 4
		}
		h.food = h.rng.Intn(width)
	}
}

// layout is how the screen is apportioned this frame. It adapts to the
// terminal: the block-font title gives way to a one-line title, the tagline
// is dropped, and the card grid becomes a compact list, in that order, so
// the library always fits - down to a quarter of a laptop screen.
type layout struct {
	big     bool // block-font title
	tagline bool
	heroH   int
	cards   bool // card grid; otherwise a one-row-per-game list
	cols    int
	tight   bool // nothing fits outright: drop the gap under the hero
}

func heroRows(big, tagline bool) int {
	n := 3 // title, underline, status
	if big {
		n = glyphRows + 3 // title block, underline, blank, status
	}
	if tagline {
		n++
	}
	return n
}

func (h *Home) layoutFor(w, hh int) layout {
	avail := hh - 2 // top margin + bottom bar
	bigOK := bigWidth(titleWord)+4 <= w

	cols := (w - 2) / (cardW + 2)
	if cols > len(h.games) {
		cols = len(h.games)
	}
	cardsOK := cols >= 1
	if cols < 1 {
		cols = 1
	}
	cardRows := 0
	if len(h.games) > 0 {
		cardRows = (len(h.games)+cols-1)/cols*(cardH+1) + 2
	}
	listRows := len(h.games) + 2

	if h.heroTarget() == 0 {
		l := layout{big: bigOK, tagline: true, cards: cardsOK, cols: cols}
		for _, c := range [][2]bool{{bigOK, true}, {false, true}, {false, false}} {
			if heroRows(c[0], c[1]) <= avail {
				l.big, l.tagline = c[0], c[1]
				break
			}
		}
		l.heroH = heroRows(l.big, l.tagline)
		if !l.cards {
			l.cols = 1
		}
		return l
	}

	type candidate struct{ big, tagline, cards bool }
	candidates := []candidate{
		{bigOK, true, cardsOK}, {bigOK, false, cardsOK},
		{false, true, cardsOK}, {false, false, cardsOK},
		{false, true, false}, {false, false, false},
	}
	for _, c := range candidates {
		rows := listRows
		if c.cards {
			rows = cardRows
		}
		if heroRows(c.big, c.tagline)+1+rows <= avail {
			l := layout{big: c.big, tagline: c.tagline, cards: c.cards, cols: cols}
			if !c.cards {
				l.cols = 1
			}
			l.heroH = heroRows(c.big, c.tagline)
			return l
		}
	}
	// Nothing fits outright: compact hero and a scrolling list.
	return layout{cards: false, cols: 1, heroH: heroRows(false, false), tight: true}
}

func (h *Home) underlineWidth() int {
	if h.lay.big {
		return bigWidth(titleWord)
	}
	return len(titleWord) + 4
}

func (h *Home) gridCols() int {
	w, hh := h.screen.Size()
	return h.layoutFor(w, hh).cols
}

func (h *Home) draw() {
	s := h.screen
	s.Clear()
	w, hh := s.Size()

	h.drawParticles(w, hh)

	h.lay = h.layoutFor(w, hh)
	heroH := h.lay.heroH
	centerY := (hh - heroH) / 2
	if centerY < 1 {
		centerY = 1
	}
	topY := 1
	if h.lay.tight {
		topY = 0
	}
	heroY := int(math.Round(float64(centerY) + (float64(topY)-float64(centerY))*h.heroShift))
	h.drawHero(w, heroY)

	if h.heroShift > 0.05 {
		listY := heroY + heroH + 1
		if h.lay.tight {
			listY--
		}
		h.drawLibrary(w, hh, listY)
	}

	if h.mode == modeSearch {
		h.drawSearch(w, hh)
	}
	h.drawToast(w)
	h.drawBottomBar(w, hh)
	s.Show()
}

func (h *Home) drawParticles(w, hh int) {
	chars := []rune{'·', '∙', '•'}
	styles := []tcell.Style{
		tcell.StyleDefault.Foreground(tcell.NewRGBColor(60, 70, 80)),
		tcell.StyleDefault.Foreground(tcell.NewRGBColor(70, 110, 120)),
		tcell.StyleDefault.Foreground(tcell.NewRGBColor(60, 160, 160)),
	}
	for _, p := range h.particles {
		x, y := int(p.x), int(p.y)
		if x < 0 || y < 0 || x >= w || y >= hh-1 {
			continue
		}
		h.screen.SetContent(x, y, chars[p.depth], nil, styles[p.depth])
	}
}

// waveColor is the title's color at column x for the current frame: a
// slow aqua↔lime wave with a brighter sweep passing through.
func (h *Home) waveColor(x, width int) tcell.Color {
	t := float64(h.frame) * 0.08
	phase := float64(x)/float64(width)*math.Pi*2 - t
	mix := (math.Sin(phase) + 1) / 2 // 0..1
	r := 0.0
	g := 255.0
	b := 255*mix + 80*(1-mix)
	// The sweep: a moving highlight ~8 cells wide.
	sweep := math.Mod(float64(h.frame)*0.9, float64(width)+30) - 15
	if d := math.Abs(float64(x) - sweep); d < 6 {
		k := 1 - d/6
		r += 200 * k
		b += 100 * k
		if b > 255 {
			b = 255
		}
	}
	return tcell.NewRGBColor(int32(r), int32(g), int32(b))
}

func (h *Home) drawHero(w, y int) {
	s := h.screen
	cx := w / 2
	st := h.status()

	// The hero sits on top of the particle field: blank its block first so
	// no speck shows through the letters' transparent cells or the text.
	heroW := h.underlineWidth() + 8
	if tw := ui.Width(tagline) + 4; tw > heroW {
		heroW = tw
	}
	if heroW > w {
		heroW = w
	}
	ui.Fill(s, cx-heroW/2, y, heroW, h.lay.heroH, tcell.StyleDefault)

	if h.lay.big {
		rows := renderBig(titleWord)
		width := bigWidth(titleWord)
		x0 := cx - width/2
		for r, row := range rows {
			for c, ch := range row {
				if ch == '#' {
					s.SetContent(x0+c, y+r, '█', nil, tcell.StyleDefault.Foreground(h.waveColor(c, width)))
				}
			}
		}
		y += glyphRows
		h.drawUnderline(x0, y, width)
		y += 2
	} else {
		ui.PrintCentered(s, cx, y, ui.StyleTitle, titleWord)
		y++
		h.drawUnderline(cx-h.underlineWidth()/2, y, h.underlineWidth())
		y++
	}

	if h.lay.tagline {
		ui.PrintCentered(s, cx, y, ui.StyleDim, ui.Truncate(tagline, w-2))
		y++
	}

	// Hub status line.
	var status string
	switch {
	case len(st.Agents) == 0:
		status = "○ no agents selected · run terminalika --setup"
	case !st.Listening:
		status = "○ listening paused · another window holds the listener seat"
	default:
		names := make([]string, 0, len(st.Agents))
		for _, a := range st.Agents {
			names = append(names, a.Name)
		}
		status = "◉ listening: " + strings.Join(names, ", ") + " · " + st.Notify
		if st.AutoPause {
			status += " · auto-pause"
		}
	}
	style := ui.StyleDim
	if st.Listening && len(st.Agents) > 0 {
		style = tcell.StyleDefault.Foreground(ui.Green)
	}
	ui.PrintCentered(s, cx, y, style, ui.Truncate(status, w-2))
}

// drawUnderline paints the rule under the title with the little snake
// chasing its food along it.
func (h *Home) drawUnderline(x, y, width int) {
	s := h.screen
	rule := tcell.StyleDefault.Foreground(tcell.NewRGBColor(50, 90, 90))
	for i := 0; i < width; i++ {
		s.SetContent(x+i, y, '─', nil, rule)
	}
	if width <= 0 {
		return
	}
	if h.food >= 0 && h.food < width {
		s.SetContent(x+h.food, y, '◆', nil, tcell.StyleDefault.Foreground(tcell.ColorRed))
	}
	for i := 0; i < h.snakeLen; i++ {
		pos := ((h.snakeHead-i)%width + width) % width
		ch, style := '━', tcell.StyleDefault.Foreground(ui.Green).Bold(true)
		if i == 0 {
			ch, style = '▶', tcell.StyleDefault.Foreground(ui.Lime).Bold(true)
		}
		s.SetContent(x+pos, y, ch, nil, style)
	}
}

func (h *Home) drawLibrary(w, hh, y int) {
	s := h.screen
	if len(h.games) == 0 {
		ui.PrintCentered(s, w/2, y, ui.StyleDim, "no games registered")
		return
	}
	// Rows reveal progressively as the hero slides up.
	reveal := int(math.Round(h.heroShift * float64(cardH)))
	if reveal <= 0 {
		return
	}

	if !h.lay.cards {
		h.drawList(w, hh, y)
		return
	}

	cols := h.lay.cols
	gridW := cols*(cardW+2) - 2
	x0 := (w - gridW) / 2
	if x0 < 0 {
		x0 = 0
	}

	rowsUsed := (len(h.games)+cols-1)/cols*(cardH+1) + 2
	if y+rowsUsed > hh-1 {
		rowsUsed = hh - 1 - y
	}
	if rowsUsed > 0 {
		ui.Fill(s, x0, y, gridW, rowsUsed, tcell.StyleDefault)
	}

	header := "retro game library"
	ui.Print(s, x0, y, ui.StyleDim, header)
	ui.HLine(s, x0+len(header)+1, y, gridW-len(header)-1, tcell.StyleDefault.Foreground(tcell.NewRGBColor(50, 70, 70)))
	y += 2

	for i, name := range h.games {
		cx := x0 + (i%cols)*(cardW+2)
		cy := y + (i/cols)*(cardH+1)
		if cy+cardH > hh-1 {
			break
		}
		h.drawCard(cx, cy, name, i == h.sel && h.mode != modeSearch, reveal)
	}
}

// drawList is the compact library for small terminals: one row per game,
// scrolled so the selection stays visible.
func (h *Home) drawList(w, hh, y int) {
	s := h.screen
	listW := 34
	if listW > w-2 {
		listW = w - 2
	}
	x0 := (w - listW) / 2
	visible := hh - 1 - y - 1 // rows left under the header
	if visible < 1 {
		return
	}
	ui.Fill(s, x0, y, listW, visible+1, tcell.StyleDefault)

	header := "games"
	ui.Print(s, x0, y, ui.StyleDim, header)
	ui.HLine(s, x0+len(header)+1, y, listW-len(header)-1, tcell.StyleDefault.Foreground(tcell.NewRGBColor(50, 70, 70)))
	y++

	start := 0
	if h.sel >= visible {
		start = h.sel - visible + 1
	}
	for i := start; i < len(h.games) && i-start < visible; i++ {
		name := h.games[i]
		row := y + i - start
		selected := i == h.sel && h.mode != modeSearch
		style := ui.StyleText
		prefix := "  "
		if selected {
			style = ui.StyleSelected
			prefix = "▶ "
			ui.Fill(s, x0, row, listW, 1, style)
		}
		ui.Print(s, x0, row, style, prefix+name)
		if best, ok := h.scores[name]; ok && best > 0 {
			txt := fmt.Sprintf("best %d", best)
			ui.Print(s, x0+listW-ui.Width(txt), row, style, txt)
		}
	}
	if start > 0 {
		ui.Print(s, x0+listW-1, y, ui.StyleDim, "↑")
	}
	if start+visible < len(h.games) {
		ui.Print(s, x0+listW-1, y+visible-1, ui.StyleDim, "↓")
	}
}

func (h *Home) drawCard(x, y int, name string, selected bool, reveal int) {
	s := h.screen
	border := tcell.StyleDefault.Foreground(tcell.NewRGBColor(70, 90, 90))
	art := tcell.StyleDefault.Foreground(tcell.NewRGBColor(120, 200, 200))
	nameStyle := ui.StyleText.Bold(true)
	if selected {
		border = ui.StyleAccent.Bold(true)
		art = tcell.StyleDefault.Foreground(ui.Lime)
		nameStyle = ui.StyleSelected
	}
	rows := cardH
	if reveal < rows {
		rows = reveal
	}
	// Draw the whole card, then blank the rows not yet revealed so the
	// card grows in from the top during the slide.
	ui.Box(s, x, y, cardW, cardH, border)
	for r, line := range previewFor(name) {
		ui.Print(s, x+2, y+1+r, art, line)
	}
	label := " " + name + " "
	if selected {
		label = " ▶ " + name + " "
	}
	ui.PrintCentered(s, x+cardW/2, y+cardH-2, nameStyle, label)
	if best, ok := h.scores[name]; ok && best > 0 {
		txt := fmt.Sprintf("best %d", best)
		ui.Print(s, x+cardW-1-len(txt)-1, y+cardH-1, ui.StyleDim, txt)
	}
	for r := rows; r < cardH; r++ {
		ui.Fill(s, x, y+r, cardW, 1, tcell.StyleDefault)
	}
}

func (h *Home) drawSearch(w, hh int) {
	s := h.screen
	bw := 46
	if bw > w-4 {
		bw = w - 4
	}
	rows := len(h.matches)
	if rows > 6 {
		rows = 6
	}
	if rows == 0 {
		rows = 1
	}
	bh := rows + 4
	x := (w - bw) / 2
	y := hh/2 - bh/2
	if y < 1 {
		y = 1
	}
	ui.Fill(s, x, y, bw, bh, tcell.StyleDefault)
	ui.Box(s, x, y, bw, bh, ui.StyleAccent)
	ui.Print(s, x+2, y+1, ui.StyleAccent.Bold(true), "› ")
	ui.Print(s, x+4, y+1, ui.StyleText.Bold(true), ui.Truncate(h.query, bw-6))
	ui.HLine(s, x+1, y+2, bw-2, tcell.StyleDefault.Foreground(tcell.NewRGBColor(50, 90, 90)))

	if len(h.matches) == 0 {
		ui.Print(s, x+2, y+3, ui.StyleDim, "no game matches")
		return
	}
	for i := 0; i < rows; i++ {
		m := h.matches[i]
		row := y + 3 + i
		matched := map[int]bool{}
		for _, p := range m.positions {
			matched[p] = true
		}
		base := ui.StyleText
		hit := tcell.StyleDefault.Foreground(ui.Lime).Bold(true)
		prefix := "  "
		if i == h.msel {
			ui.Fill(s, x+1, row, bw-2, 1, ui.StyleSelected)
			base = ui.StyleSelected
			hit = ui.StyleSelected.Underline(true)
			prefix = "▶ "
		}
		xx := ui.Print(s, x+2, row, base, prefix)
		for j, r := range []rune(m.name) {
			st := base
			if matched[j] {
				st = hit
			}
			s.SetContent(xx, row, r, nil, st)
			xx++
		}
		if i == 0 {
			ui.Print(s, x+bw-8, row, base, "Enter")
		}
	}
}

func (h *Home) drawToast(w int) {
	if h.toast == nil {
		return
	}
	s := h.screen
	ev := h.toast.ev
	lines := []string{ev.Message()}
	width := 0
	for _, l := range lines {
		if n := ui.Width(l); n > width {
			width = n
		}
	}
	bw := width + 4
	if bw > w-2 {
		bw = w - 2
	}
	x := w - bw - 1
	if x < 1 {
		x = 1
	}
	style := toastStyle(ev.Agent.ID)
	ui.Fill(s, x, 1, bw, len(lines)+2, style)
	for i, l := range lines {
		ui.Print(s, x+2, 2+i, style, ui.Truncate(l, bw-4))
	}
}

// toastStyle mirrors the engine's per-agent overlay colors so an event
// looks the same on the home screen as inside a game.
func toastStyle(id agents.ID) tcell.Style {
	switch id {
	case agents.Claude:
		return tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorOrange).Bold(true)
	case agents.Pi:
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorIndigo).Bold(true)
	case agents.Aider:
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkGreen).Bold(true)
	case agents.Cursor:
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorNavy).Bold(true)
	}
	return ui.StyleSelected
}

func (h *Home) drawBottomBar(w, hh int) {
	s := h.screen
	y := hh - 1
	ui.Fill(s, 0, y, w, 1, tcell.StyleDefault.Background(tcell.NewRGBColor(18, 24, 28)))
	bar := tcell.StyleDefault.Foreground(ui.Dim).Background(tcell.NewRGBColor(18, 24, 28))
	key := tcell.StyleDefault.Foreground(ui.Accent).Background(tcell.NewRGBColor(18, 24, 28)).Bold(true)

	var text string
	switch h.mode {
	case modeSearch:
		text = "↑/↓ choose   Enter play   Esc cancel"
	case modeExplore:
		text = "←/→/↑/↓ choose   Enter play   ↑ from top row: back   type to search   Esc back   q quit"
	default:
		text = Prompt
		if ui.Width(text)+2 > w {
			text = shortPrompt
		}
	}
	text = ui.Truncate(text, w-2)
	x := w/2 - ui.Width(text)/2
	// Highlight the bracketed keys.
	inKey := false
	for _, r := range text {
		st := bar
		if r == '[' {
			inKey = true
		}
		if inKey {
			st = key
		}
		s.SetContent(x, y, r, nil, st)
		if r == ']' {
			inKey = false
		}
		x++
	}

	// The last agent event sits right-aligned on the row above the bar, so
	// it never collides with the prompt on a narrow terminal.
	if h.feed == nil || h.mode == modeSearch || hh <= 6 {
		return
	}
	if last, ok := h.feed.Latest(); ok {
		txt := ui.Truncate("last event · "+last.At.Format("15:04")+" "+last.Title(), w-2)
		ui.Fill(s, w-1-ui.Width(txt)-1, y-1, ui.Width(txt)+2, 1, tcell.StyleDefault)
		ui.Print(s, w-1-ui.Width(txt), y-1, ui.StyleDim, txt)
	}
}

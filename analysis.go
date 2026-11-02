package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// testTimeout bounds Vyzkoušet, well under the web server's write timeout
// (a variable so tests can shorten it).
var testTimeout = 10 * time.Second

const (
	askTimeout      = 2 * time.Minute // one question during the hourly look
	contextMessages = 50              // earlier messages given as context (criterion 15)
	batchMessages   = 200             // new messages per question, so long histories fit
	defaultLimit    = 200             // questions per day (criterion 19)
	analysisEvery   = time.Hour
)

// rawFinding is what an analyst reports before it is stored.
type rawFinding struct {
	SituationID int64
	Reason      string
	Quotes      []numbered
}

// analyst looks at one chat's messages for the given situations.
type analyst interface {
	Analyze(ctx context.Context, chat chatInfo, old, fresh []numbered, sits []Situation) ([]rawFinding, error)
}

type chatInfo struct {
	Name    string
	IsGroup bool
}

// analyzer runs the hourly look (criteria 15–20) and remembers how it went.
type analyzer struct {
	st         *store
	lang       *lang
	zone       *time.Location
	secretsDir string
	practice   *practiceModel // non-nil only in practice mode

	run sync.Mutex // one look at a time

	mu      sync.Mutex
	lastErr string
	limited string // the day the limit was hit, "" if not
	lastRun time.Time
	hourly  bool // the hourly loop runs (not in practice mode)
}

// analysisState is what the overview says about the analysis.
type analysisState struct {
	NoModel bool
	Error   string
	Limited bool
	LastRun time.Time // zero before the first look
	NextRun time.Time // zero when there is no hourly loop
}

func (z *analyzer) state(ctx context.Context) analysisState {
	m := z.settings(ctx)
	if !m.ready() {
		return analysisState{NoModel: true}
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	st := analysisState{Error: z.lastErr, Limited: z.limited == z.today(), LastRun: z.lastRun}
	if z.hourly && !z.lastRun.IsZero() {
		st.NextRun = z.lastRun.Add(analysisEvery)
	}
	return st
}

func (z *analyzer) today() string { return time.Now().In(z.zone).Format("2006-01-02") }

// settings reads Nastavení; the key comes from secrets/ only.
func (z *analyzer) settings(ctx context.Context) modelSettings {
	get := func(k string) string { v, _ := z.st.Setting(ctx, k); return v }
	m := modelSettings{Type: get("model_type"), Address: get("model_address"), Model: get("model_model"), Limit: defaultLimit}
	if m.Type == "" {
		m.Type, m.Address, m.Model = modelAnthropic, defaultAnthropicAddress, defaultAnthropicModel
	}
	if m.Type == modelPractice && z.practice == nil {
		m.Type = "" // the practice model exists only in practice mode
	}
	if n, err := strconv.Atoi(get("model_limit")); err == nil && n >= 0 {
		m.Limit = n
	}
	if b, err := os.ReadFile(filepath.Join(z.secretsDir, "model-key")); err == nil {
		m.Key = strings.TrimSpace(string(b))
	}
	return m
}

func (z *analyzer) saveSettings(ctx context.Context, m modelSettings, newKey string, forgetKey bool) error {
	for k, v := range map[string]string{"model_type": m.Type, "model_address": m.Address, "model_model": m.Model, "model_limit": strconv.Itoa(m.Limit)} {
		if err := z.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	if forgetKey {
		if err := os.Remove(filepath.Join(z.secretsDir, "model-key")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if newKey != "" {
		if err := ensureDir(z.secretsDir); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(z.secretsDir, "model-key"), []byte(newKey), 0o600); err != nil {
			return err
		}
	}
	z.mu.Lock()
	z.lastErr, z.limited = "", ""
	z.mu.Unlock()
	return nil
}

func (z *analyzer) analystFor(m modelSettings) (analyst, error) {
	if m.Type == modelPractice {
		return z.practice, nil
	}
	cm, err := newChatModel(m)
	if err != nil {
		return nil, err
	}
	return llmAnalyst{model: cm, lang: z.lang}, nil
}

// test asks the given model a short question (the Vyzkoušet button).
func (z *analyzer) test(ctx context.Context, m modelSettings) error {
	if m.Type == modelPractice && z.practice == nil {
		m.Type = ""
	}
	if miss := z.missing(m); miss != "" {
		return errMissing{miss}
	}
	if m.Type == modelPractice {
		return z.practice.check()
	}
	cm, err := newChatModel(m)
	if err != nil {
		return err
	}
	// Well under the web server's write timeout, so the page always answers.
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	answer, err := cm.Ask(ctx, "Answer with the single word OK.", "Are you there?")
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) == "" {
		return errors.New(z.lang.T("model_empty"))
	}
	return nil
}

// loop runs the look once a minute after start, then every hour.
func (z *analyzer) loop(ctx context.Context) {
	z.mu.Lock()
	z.hourly = true
	z.mu.Unlock()
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			z.runOnce(ctx)
			t.Reset(analysisEvery)
		}
	}
}

// runOnce looks at every chat with new messages, within the daily limit.
func (z *analyzer) runOnce(ctx context.Context) {
	z.run.Lock()
	defer z.run.Unlock()
	m := z.settings(ctx)
	if !m.ready() {
		return
	}
	a, err := z.analystFor(m)
	if err != nil {
		z.fail(err)
		return
	}
	sits, err := z.st.Situations(ctx)
	if err != nil {
		z.fail(err)
		return
	}
	var on []Situation
	for _, s := range sits {
		if s.Enabled {
			on = append(on, s)
		}
	}
	chats, err := z.st.PendingChats(ctx)
	if err != nil {
		z.fail(err)
		return
	}
	for _, chat := range chats {
		if err := z.lookAt(ctx, a, m, chat, on); err != nil {
			if errors.Is(err, errLimit) {
				z.mu.Lock()
				z.limited, z.lastRun = z.today(), time.Now()
				z.mu.Unlock()
				return
			}
			z.fail(err)
			return
		}
	}
	z.mu.Lock()
	z.lastErr, z.lastRun = "", time.Now()
	z.mu.Unlock()
}

var errLimit = errors.New("daily limit reached")

func (z *analyzer) fail(err error) {
	log.Printf("analysis: %v", err)
	z.mu.Lock()
	z.lastErr = z.lang.explain(err)
	z.lastRun = time.Now()
	z.mu.Unlock()
}

// noteTest lets a Vyzkoušet of the saved model update the overview.
func (z *analyzer) noteTest(err error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err != nil {
		z.lastErr = z.lang.explain(err)
	} else {
		z.lastErr = ""
	}
}

func (z *analyzer) lookAt(ctx context.Context, a analyst, m modelSettings, chat string, on []Situation) error {
	old, fresh, err := z.st.ChatWindow(ctx, chat, contextMessages)
	if err != nil {
		return err
	}
	if len(fresh) == 0 {
		return nil
	}
	if len(on) == 0 { // nothing to watch for: nothing to send anywhere
		return z.st.MarkAnalyzed(ctx, chat, fresh[len(fresh)-1].Seq)
	}
	info := chatInfo{}
	info.Name, _ = z.st.ChatName(ctx, chat)
	info.IsGroup = strings.HasSuffix(chat, "@g.us") || z.chatIsGroup(ctx, chat)
	for len(fresh) > 0 {
		batch := fresh
		if len(batch) > batchMessages {
			batch = fresh[:batchMessages]
		}
		if err := z.spend(ctx, m.Limit); err != nil {
			return err
		}
		askCtx, cancel := context.WithTimeout(ctx, askTimeout)
		found, err := a.Analyze(askCtx, info, old, batch, on)
		cancel()
		if err != nil {
			return err
		}
		for _, f := range found {
			quotes := make([]Quote, 0, len(f.Quotes))
			for _, q := range f.Quotes {
				quotes = append(quotes, Quote{SenderName: q.SenderName, FromMe: q.FromMe, At: q.At, Text: q.Text})
			}
			if err := z.st.RecordFinding(ctx, chat, f.SituationID, f.Reason, quotes, time.Now()); err != nil {
				return err
			}
		}
		if err := z.st.MarkAnalyzed(ctx, chat, batch[len(batch)-1].Seq); err != nil {
			return err
		}
		old = append(old, batch...)
		if len(old) > contextMessages {
			old = old[len(old)-contextMessages:]
		}
		fresh = fresh[len(batch):]
	}
	return nil
}

func (z *analyzer) chatIsGroup(ctx context.Context, chat string) bool {
	var g bool
	_ = z.st.db.QueryRowContext(ctx, "SELECT is_group FROM chats WHERE jid = ?", chat).Scan(&g)
	return g
}

// spend counts one question against today's limit, or says the limit is hit.
func (z *analyzer) spend(ctx context.Context, limit int) error {
	key := "usage:" + z.today()
	v, _ := z.st.Setting(ctx, key)
	n, _ := strconv.Atoi(v)
	if n >= limit {
		return errLimit
	}
	return z.st.SetSetting(ctx, key, strconv.Itoa(n+1))
}

// llmAnalyst asks a real model, with the situations written into the prompt.
type llmAnalyst struct {
	model chatModel
	lang  *lang
}

func (l llmAnalyst) Analyze(ctx context.Context, chat chatInfo, old, fresh []numbered, sits []Situation) ([]rawFinding, error) {
	system := fmt.Sprintf(analysisPrompt, l.lang.name)
	var b strings.Builder
	b.WriteString("SITUATIONS\n")
	for _, s := range sits {
		fmt.Fprintf(&b, "id %d | %s | sensitivity %s | %s\n", s.ID, s.Name, s.Sensitivity, oneLine(s.Description))
	}
	kind := "a direct chat"
	if chat.IsGroup {
		kind = "a group chat"
	}
	fmt.Fprintf(&b, "\nCHAT: %q, %s\n", chat.Name, kind)
	n := 0
	line := func(m numbered) {
		n++
		who := m.SenderName
		if m.FromMe {
			who = "CHILD"
		} else if who == "" {
			who = "someone"
		}
		text := oneLine(m.Text)
		if m.Kind != "text" {
			text = "<" + m.Kind + "> " + text
		}
		fmt.Fprintf(&b, "[%d] %s %s: %s\n", n, m.At.UTC().Format("2006-01-02 15:04"), who, text)
	}
	b.WriteString("\nEARLIER (context)\n")
	for _, m := range old {
		line(m)
	}
	first := n + 1
	b.WriteString("\n---NEW---\n")
	for _, m := range fresh {
		line(m)
	}
	answer, err := l.model.Ask(ctx, system, b.String())
	if err != nil {
		return nil, err
	}
	return parseFindings(answer, sits, fresh, first)
}

const analysisPrompt = `You help parents protect their child from bullying and online harm, with the child's knowledge.
You read one of the child's chats and look for the situations listed. The child's own messages are marked CHILD.
Look for patterns: repetition, who writes to whom, exclusion, changes in tone. Friendly teasing between friends is not bullying.
Messages before ---NEW--- are context you have seen before; report only what the NEW messages show, using the context to see a pattern.
Sensitivity: high = report even faint signs; medium = report when it is reasonably likely; low = report only clear cases.
For each situation found, write the reason in %s, in one or two calm sentences that the child may read too: say what happened, never judge or blame the child.
Quote at most two message numbers from the NEW messages that show it.
Answer with JSON only, no other text: {"findings":[{"situation":<id>,"reason":"...","quotes":[<number>]}]}, or {"findings":[]} when nothing applies.`

// parseFindings reads the model's JSON; quotes are taken from the stored
// messages by number, so a model cannot invent a quote.
func parseFindings(answer string, sits []Situation, fresh []numbered, first int) ([]rawFinding, error) {
	i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if i < 0 || j < i {
		return nil, errors.New("the model's answer could not be read")
	}
	var out struct {
		Findings []struct {
			Situation int64  `json:"situation"`
			Reason    string `json:"reason"`
			Quotes    []int  `json:"quotes"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(answer[i:j+1]), &out); err != nil {
		return nil, errors.New("the model's answer could not be read")
	}
	known := map[int64]bool{}
	for _, s := range sits {
		known[s.ID] = true
	}
	var res []rawFinding
	for _, f := range out.Findings {
		if !known[f.Situation] || strings.TrimSpace(f.Reason) == "" {
			continue
		}
		r := rawFinding{SituationID: f.Situation, Reason: strings.TrimSpace(f.Reason)}
		for _, q := range f.Quotes {
			if k := q - first; k >= 0 && k < len(fresh) && len(r.Quotes) < 2 {
				r.Quotes = append(r.Quotes, fresh[k])
			}
		}
		res = append(res, r)
	}
	return res, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// practiceModel is the model of criterion 21: it reports a situation when a
// new message contains the situation's name in square brackets.
type practiceModel struct {
	mu          sync.Mutex
	unreachable bool
	reason      string
	errText     string
}

func (p *practiceModel) check() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unreachable {
		return errors.New(p.errText)
	}
	return nil
}

func (p *practiceModel) Analyze(ctx context.Context, chat chatInfo, old, fresh []numbered, sits []Situation) ([]rawFinding, error) {
	if err := p.check(); err != nil {
		return nil, err
	}
	var res []rawFinding
	for _, s := range sits {
		tag := "[" + s.Name + "]"
		var f *rawFinding
		for _, m := range fresh {
			if !strings.Contains(m.Text, tag) {
				continue
			}
			if f == nil {
				res = append(res, rawFinding{SituationID: s.ID, Reason: p.reason})
				f = &res[len(res)-1]
			}
			if len(f.Quotes) < 2 {
				f.Quotes = append(f.Quotes, m)
			}
		}
	}
	return res, nil
}

// errMissing says which field is empty; nothing was tried.
type errMissing struct{ text string }

func (e errMissing) Error() string { return e.text }

// missing names the first thing that keeps the model from being asked.
func (z *analyzer) missing(m modelSettings) string {
	switch {
	case m.Type == modelPractice || m.ready():
		return ""
	case m.Type == modelAnthropic && m.Key == "":
		return z.lang.T("miss_key")
	case m.Address == "":
		return z.lang.T("miss_address")
	case m.Model == "":
		return z.lang.T("miss_model")
	}
	return z.lang.T("miss_key")
}

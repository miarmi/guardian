package guardian

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The hourly look (criteria 15–20): every chat with new messages goes to
// the model with some earlier context, within a daily limit of questions.
// What is asked lives in analyst.go, how in model.go.

// testTimeout bounds Vyzkoušet and the e-mails sent from Nastavení, well
// under the web server's write timeout so the page always answers (a
// variable so tests can shorten it).
var testTimeout = 10 * time.Second

const (
	askTimeout      = 2 * time.Minute // one question during the hourly look
	contextMessages = 50              // earlier messages given as context (criterion 15)
	batchMessages   = 200             // new messages per question, so long histories fit
	defaultLimit    = 200             // questions per day (criterion 19)
	analysisEvery   = time.Hour
)

// analyzer runs the hourly look and remembers how it went.
type analyzer struct {
	st          *store
	lang        *lang
	zone        *time.Location
	secretsDir  string
	practice    *practiceModel // non-nil only in practice mode
	notify      func(ctx context.Context, findingID int64, isNew bool)
	afterLook   func(ctx context.Context, start time.Time) // e.g. the child's e-mails
	box         mailbox                                    // Gmail (criterion 33)
	practiceBox *practiceBox                               // the practice mailbox (criterion 36), practice mode only

	run sync.Mutex // one look at a time

	mu       sync.Mutex
	gmailErr string // why Gmail couldn't be read at the last look
	lastErr  string
	limited  string // the day the limit was hit, "" if not
	lastRun  time.Time
	hourly   bool // the hourly loop runs (not in practice mode)
}

// analysisState is what the overview says about the analysis.
type analysisState struct {
	NoModel bool
	Error   string
	Limited bool
	LastRun time.Time // zero before the first look
	NextRun time.Time // zero when there is no hourly loop
}

// state says how the analysis stands with the settings m.
func (z *analyzer) state(m modelSettings) analysisState {
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

var errLimit = errors.New("daily limit reached")

// runOnce looks at every chat with new messages, within the daily limit.
func (z *analyzer) runOnce(ctx context.Context) {
	z.run.Lock()
	defer z.run.Unlock()
	if z.afterLook != nil {
		defer z.afterLook(ctx, time.Now())
	}
	m := z.settings(ctx)
	if !m.ready() {
		return
	}
	a, err := z.analystFor(m)
	if err != nil {
		z.fail(err)
		return
	}
	on, err := z.enabledSituations(ctx)
	if err != nil {
		z.fail(err)
		return
	}
	chats, err := z.st.PendingChats(ctx)
	if err != nil {
		z.fail(err)
		return
	}
	for _, chat := range chats {
		err := z.lookAt(ctx, a, m, chat, on)
		switch {
		case errors.Is(err, errLimit):
			z.mu.Lock()
			z.limited, z.lastRun = z.today(), time.Now()
			z.mu.Unlock()
			return
		case err != nil:
			z.fail(err)
			return
		}
	}
	switch err := z.readParentalMail(ctx, a, m, on); {
	case errors.Is(err, errLimit):
		z.mu.Lock()
		z.limited, z.lastRun = z.today(), time.Now()
		z.mu.Unlock()
		return
	case err != nil:
		z.fail(err)
		return
	}
	z.mu.Lock()
	z.lastErr, z.lastRun = "", time.Now()
	z.mu.Unlock()
}

func (z *analyzer) enabledSituations(ctx context.Context) ([]Situation, error) {
	sits, err := z.st.Situations(ctx)
	if err != nil {
		return nil, err
	}
	var on []Situation
	for _, s := range sits {
		// The photo situation is judged inside Guardian, never by the model.
		if s.Enabled && s.Kind == "" {
			on = append(on, s)
		}
	}
	return on, nil
}

func (z *analyzer) fail(err error) {
	log.Printf("analysis: %v", err)
	z.mu.Lock()
	z.lastErr = z.lang.explain(err)
	z.lastRun = time.Now()
	z.mu.Unlock()
}

// lookAt asks about one chat's new messages, batchMessages at a time, and
// moves the chat's cursor after each batch so nothing is asked twice.
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
	info, _ := z.st.ChatInfo(ctx, chat)
	info.IsGroup = info.IsGroup || strings.HasSuffix(chat, "@g.us")
	for len(fresh) > 0 {
		batch := fresh[:min(len(fresh), batchMessages)]
		if err := z.spend(ctx, m.Limit); err != nil {
			return err
		}
		askCtx, cancel := context.WithTimeout(ctx, askTimeout)
		found, err := a.Analyze(askCtx, info, old, batch, on)
		cancel()
		if err != nil {
			return err
		}
		if err := z.record(ctx, chat, found); err != nil {
			return err
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

// record stores what the analyst found and tells the notifier.
func (z *analyzer) record(ctx context.Context, chat string, found []rawFinding) error {
	for _, f := range found {
		quotes := make([]Quote, 0, len(f.Quotes))
		for _, q := range f.Quotes {
			quotes = append(quotes, Quote{SenderName: q.SenderName, FromMe: q.FromMe, At: q.At, Text: q.Text})
		}
		id, isNew, err := z.st.RecordFinding(ctx, chat, f.SituationID, f.Reason, quotes, time.Now())
		if err != nil {
			return err
		}
		if z.notify != nil {
			z.notify(ctx, id, isNew)
		}
	}
	return nil
}

// spend counts one question against today's limit, or says the limit is hit.
// Looks never overlap (run), so reading and writing the count is safe.
func (z *analyzer) spend(ctx context.Context, limit int) error {
	key := "usage:" + z.today()
	v, _ := z.st.Setting(ctx, key)
	n, _ := strconv.Atoi(v)
	if n >= limit {
		return errLimit
	}
	return z.st.SetSetting(ctx, key, strconv.Itoa(n+1))
}

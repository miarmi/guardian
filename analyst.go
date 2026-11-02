package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// llmAnalyst asks a real model, with the situations written into the prompt.
type llmAnalyst struct {
	model chatModel
	lang  *lang
}

func (l llmAnalyst) Analyze(ctx context.Context, chat chatInfo, old, fresh []numbered, sits []Situation) ([]rawFinding, error) {
	question, first := analysisQuestion(chat, old, fresh, sits)
	answer, err := l.model.Ask(ctx, fmt.Sprintf(analysisPrompt, l.lang.name), question)
	if err != nil {
		return nil, err
	}
	return parseFindings(answer, sits, fresh, first)
}

// analysisQuestion lists the situations and numbers the messages, earlier
// ones first. first is the number of the first new message.
func analysisQuestion(chat chatInfo, old, fresh []numbered, sits []Situation) (question string, first int) {
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
	first = n + 1
	b.WriteString("\n---NEW---\n")
	for _, m := range fresh {
		line(m)
	}
	return b.String(), first
}

const analysisPrompt = `You help parents protect their child from bullying and online harm, with the child's knowledge.
You read one of the child's chats and look for the situations listed. The child's own messages are marked CHILD.
Most messages are normal life between children. Your default answer is no finding.
Judge patterns, not single words: look at who does what to whom, how often, and how the child reacts.
Harm is usually one-sided and repeated, or it involves a power gap: an adult, a group against one child, threats, demands for secrecy.
Mutual teasing, irony, swearing that both sides use, emojis, slang, gaming trash talk, memes and dark humour are not harm when both take part and nobody objects.
A single rude message that is answered or resolved is not a finding. Exaggeration ("I'll kill you", "I'm dying") is not a threat unless it is concrete or the other person is scared.
Never report the child for ordinary things: crushes, swearing, complaining about school, arguing with friends, being silly.
For grooming, sextortion, pressure for intimate photos, self-harm, threats of violence, meeting someone from the internet and dangerous groups, one clear message is enough. For the others, there must be a pattern.
Report only what a careful, reasonable parent would truly want to know. When unsure at the given sensitivity, report nothing.
Messages before ---NEW--- are context you have seen before; report only what the NEW messages show, using the context to see a pattern.
Sensitivity: low = only clear, explicit signs; medium = clear signs or a strong pattern; high = faint but specific signs.
For each situation found, write the reason in %s, in one or two calm, kind sentences that the child may read too: say what happened, never label or blame anyone, and never suggest the child did wrong when they are the target.
Quote at most two message numbers from the NEW messages that show the problem itself.
Answer with JSON only, no other text: {"findings":[{"situation":<id>,"reason":"...","quotes":[<number>]}]}, or {"findings":[]} when nothing applies.`

var errUnreadable = errors.New("the model's answer could not be read")

// parseFindings reads the model's JSON; quotes are taken from the stored
// messages by number, so a model cannot invent a quote.
func parseFindings(answer string, sits []Situation, fresh []numbered, first int) ([]rawFinding, error) {
	i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if i < 0 || j < i {
		return nil, errUnreadable
	}
	var out struct {
		Findings []struct {
			Situation int64  `json:"situation"`
			Reason    string `json:"reason"`
			Quotes    []int  `json:"quotes"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(answer[i:j+1]), &out); err != nil {
		return nil, errUnreadable
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

func (p *practiceModel) setUnreachable(on bool) {
	p.mu.Lock()
	p.unreachable = on
	p.mu.Unlock()
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

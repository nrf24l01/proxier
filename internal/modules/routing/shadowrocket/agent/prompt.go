package agent

import (
	"fmt"
	"strings"
	"time"
)

// PromptData is what the prompt is made from.
type PromptData struct {
	Agent   string
	Config  string // the file name, iphone.conf
	Problem string
	BaseURL string // Proxier's address, without the API path
	Token   string
	Expires time.Time
}

// opening is the only line that differs between agents.
var opening = map[string]string{
	"claude-code": "You are Claude Code. The owner of a Proxier instance has handed you the base config of one of its hosted Shadowrocket configs to change.",
	"codex":       "You are Codex. The owner of a Proxier instance has handed you the base config of one of its hosted Shadowrocket configs to change.",
	"other":       "You are a coding agent. The owner of a Proxier instance has handed you the base config of one of its hosted Shadowrocket configs to change.",
}

// Prompt is the Markdown the member pastes into the agent. It holds the real
// token: show it masked with agentapi.Mask, copy it whole.
func Prompt(d PromptData) string {
	api := strings.TrimRight(d.BaseURL, "/") + APIPrefix
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("%s\n\n", opening[d.Agent])
	w("# Task: change the base config of the Shadowrocket config %q\n\n", d.Config)
	w("## The problem\n\n%s\n\n", d.Problem)

	w("## Access\n\n")
	w("- API: `%s`\n", api)
	w("- Every request carries `Authorization: Bearer %s`\n", d.Token)
	w("- The token reaches this one base config and nothing else, and stops working at %s UTC (or earlier if the owner revokes it).\n", d.Expires.UTC().Format("2006-01-02 15:04"))
	w("- The token is a secret. Do not print it, log it, or write it into any file.\n\n")

	w("## The config\n\n")
	w("The base config is a hand-written Shadowrocket config (`[General]`, `[Rule]`, `[Host]`, `[URL Rewrite]`, `[MITM]` and so on). An iPhone subscribes to its URL. On every fetch Proxier copies the base byte for byte and inserts the rules of a routing list into `[Rule]`: before its first `FINAL,` line, or at the end of the section followed by `FINAL,DIRECT`. The base must keep its `[Rule]` section. Do not write those inserted rules into the base: `GET /output` shows what the phone gets with them.\n\n")

	w("## API\n\n")
	w("| Request | Does |\n|---|---|\n")
	w("| `GET /context` | The problem, the config's name, routing list, rule policy and base version, the check of the base and where its hidden values are. Start here. |\n")
	w("| `GET /base` | The whole base config as text, with its `ETag`. |\n")
	w("| `PUT /base` | Replace the base config (the request body is the whole file). Send `If-Match: <the ETag>`. A stale ETag gets `412`: read again and redo the edit. Add `?note=` (at most 200 characters) to say what the version changes. The answer has the new ETag and the check. |\n")
	w("| `POST /base/check` | Check the saved base (empty body), or the base in the body without saving it: what a save would refuse, and where Proxier inserts its rules. |\n")
	w("| `GET /output` | What the phone gets now: the saved base with the routing list's rules inserted. |\n\n")
	w("Every save changes the ETag. The base is at most 256 KiB of UTF-8 text, at most 60 requests a minute and 600 in all. A `401` with \"session ended\" means the access is over.\n\n")

	w("## Rules\n\n")
	w("- Work only through this API. You cannot change the config's routing list, policy or URL, or touch anything else, and you must not try.\n")
	w("- **Every save is live.** It becomes the next base version, and the phone gets it on its next refresh. Save only a base you have checked; the owner can restore an older version.\n")
	w("- Secret values are never visible: every `[Proxy]` line's value and `[MITM]`'s `ca-p12` and `ca-passphrase` read as `%s`. Leave that value as it is, under the same key in the same section, and Proxier keeps the real one; do not try to recover it.\n", Masked)
	w("- Edit the smallest thing that fixes the problem. Keep what the owner did not ask you to change, comments and line order included.\n")
	w("- The config must stay valid for Shadowrocket. You cannot run it on the phone; reason carefully about Shadowrocket's syntax.\n")
	w("- Finish by saying what you changed and why, the versions you saved, and anything that still needs the owner's attention.\n")
	return b.String()
}

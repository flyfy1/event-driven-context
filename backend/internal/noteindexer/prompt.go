package noteindexer

import (
	_ "embed"
	"fmt"
	"strings"
)

const PromptVersion = "1"
const Model = "gpt-5.6-luna"
const Reasoning = "medium"

//go:embed prompts/system.md
var systemPrompt string

func seedDefaults(files map[string]string) {
	policies := map[string]string{
		"daily":   "Organize by year/month/day in project timezone; use occurred_at, falling back to recorded_at.\n- Summarize chronologically; late evidence and corrections revise the relevant day.\n- Preserve meaningful history and cite sources beside claims.",
		"persons": "Organize by stable person identity, then facets such as roles, preferences, relationships, and background.\n- Do not merge similar names without evidence or turn inferred traits into facts.\n- Start with a profile; split facets only when content becomes crowded.",
		"topics":  "Organize reusable knowledge beneath work/ and life/, then by subject.\n- Explain concepts and lessons independently of a specific goal's status.\n- Link cross-cutting topics to one primary home; split crowded subjects into smaller notes.",
		"goals":   "Organize desired outcomes, including work and life goals.\n- Keep goals/priorities.md a short ranked view of current attention and its reasons.\n- Add tasks, todos, resources, timeline, and subgoals only when evidence warrants them.\n- Break large goals into independently understandable outcomes; proposed actions remain proposals.",
	}
	for lens, body := range policies {
		name := lens + "/organization.md"
		if _, ok := files[name]; !ok {
			files[name] = fmt.Sprintf("---\nschema_version: 1\nlens: %s\nbody_style: bullet_points\norganization_word_limit: 499\ndefault_note_word_limit: 999\n---\n- %s\n- Use short branch indexes with links to immediate children.\n- Adapt these bullets to observed data while respecting fixed constraints.\n", lens, body)
		}
	}
	indexes := map[string]string{"index.md": "Project notes", "daily/index.md": "Daily", "persons/index.md": "Persons", "topics/index.md": "Topics", "topics/work/index.md": "Work topics", "topics/life/index.md": "Life topics", "goals/index.md": "Goals", "goals/priorities.md": "Current priorities"}
	for name, title := range indexes {
		if _, ok := files[name]; !ok {
			id := "note_" + strings.NewReplacer("/", "_", ".md", "").Replace(name)
			files[name] = fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n# %s\n\n> Summary: No information has been organized here yet.\n", id, title, title)
		}
	}
}

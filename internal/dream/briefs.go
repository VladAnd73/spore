package dream

import _ "embed"

// The briefs are the prompt half of the feature: the code decides which
// sessions get read, and these decide what the reading agent does with
// them. They are data, so they live in files and ship embedded.

//go:embed briefs/proposer.md
var ProposerBrief string

//go:embed briefs/reviewer.md
var ReviewerBrief string

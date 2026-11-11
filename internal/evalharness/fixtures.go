package evalharness

import "embed"

// FixturesFS embeds this package's fixture scenarios (proposer
// transcripts, reviewer packets, and the deliberately broken brief used
// to prove the override mechanism swaps content) into the spore
// binary, the same way internal/dream embeds its briefs, so `spore
// eval run <scenario>` works from the binary alone.
//
//go:embed fixtures
var FixturesFS embed.FS

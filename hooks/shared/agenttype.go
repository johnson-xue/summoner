package shared

// agenttype.go — the framework's delegated agent types and their plugin
// namespace (fix-round C1). Platform fact (verified live 2026-11-11): plugin
// agents invoke as <plugin>:<name>, so Summoner's reviewer/worker reach the
// gate as "summoner:reviewer" / "summoner:worker" in production; the bare
// names remain valid for tests and back-compat. The prefix is the framework's
// OWN fixed plugin name — never project data and never a wildcard: any other
// namespace ("other:reviewer", "summoner:fork", "summoner:unregistered")
// stays a foreign type.

// SummonerPluginPrefix is the plugin namespace the framework's agents are
// registered under. It is a fixed constant of the framework, not read from
// any project manifest.
const SummonerPluginPrefix = "summoner:"

// NormalizeAgentType maps a platform agent type to the canonical framework
// delegation type: "summoner:reviewer" → "reviewer", "summoner:worker" →
// "worker"; every other spelling (including the bare forms, which are already
// canonical) is returned unchanged, so callers keep denying foreign types
// with their existing allow-list lookup.
func NormalizeAgentType(typ string) string {
	if len(typ) > len(SummonerPluginPrefix) && typ[:len(SummonerPluginPrefix)] == SummonerPluginPrefix {
		return typ[len(SummonerPluginPrefix):]
	}
	return typ
}

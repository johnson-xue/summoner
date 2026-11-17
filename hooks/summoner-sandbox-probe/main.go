// summoner-sandbox-probe — the IN-BAND sandbox-effect probe adapter (T19, spec
// §3 Bash 副作用信任阶梯 L2). This is NOT a hook: hooks.json never wires it,
// no hook ever execs it. It runs as a MODEL Bash call — inside the sandbox,
// the one place a probe result says anything about the model's confinement —
// and prints exactly one wrapper line:
//
//	{"probe":"summoner-sandbox-probe/v1","effect":"refused","detail":"…"}
//
// posttooluse observes that wrapper ONLY for calls the gate marked as the
// probe channel (the anchored `${CLAUDE_PLUGIN_ROOT}/hooks/bin/
// summoner-sandbox-probe` invocation, no operands — IsSandboxProbeInvocation),
// and the L2 admission requires the latest observation to be "refused": the
// fresh exclusive HOME write the sandbox must refuse (a sandboxed environment
// denies writes outside the workspace, and $HOME is outside it).
//
// exit 0 ALWAYS, whatever the effect — an unproven or writable probe is an
// honest observation, never a command failure. The probe writes ONLY its own
// fresh exclusively-created file and removes only that file (the fixed-path
// write of the old detector never happens here).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	res := shared.SandboxProbeResult{Effect: shared.SandboxProbeUnproven, Detail: "no home directory"}
	if home, err := os.UserHomeDir(); err == nil {
		res = shared.RunSandboxProbe(home)
	}
	wrapper := struct {
		Probe  string `json:"probe"`
		Effect string `json:"effect"`
		Detail string `json:"detail,omitempty"`
	}{
		Probe:  shared.SandboxProbeSignature,
		Effect: res.Effect,
		Detail: res.Detail,
	}
	b, err := json.Marshal(wrapper)
	if err != nil {
		// Cannot happen for these value types; fail honestly if it ever does.
		fmt.Fprintf(os.Stderr, "summoner-sandbox-probe: wrapper marshal failed: %v\n", err)
		os.Exit(0)
	}
	fmt.Println(string(b))
}

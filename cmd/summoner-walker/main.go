package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"

	"github.com/johnson-xue/summoner/internal/graph"
	"github.com/spf13/cobra"
)

var (
	graphFile string
	traceFile string
	sessionID string
)

func main() {
	root := &cobra.Command{Use: "summoner-walker", Short: "Summoner graph walker (router + bookkeeper)"}
	root.PersistentFlags().StringVar(&graphFile, "graph", "", "plan.md path or raw graph YAML path")
	root.PersistentFlags().StringVar(&traceFile, "trace", "", "trace.jsonl path (append-only)")
	root.PersistentFlags().StringVar(&sessionID, "session", "default", "session id (walk-state key)")

	root.AddCommand(nextCmd())
	root.AddCommand(recordCmd())
	root.AddCommand(explainCmd())
	root.AddCommand(statusCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadGraph() (*graph.Graph, error) {
	b, err := ioutil.ReadFile(graphFile)
	if err != nil {
		return nil, err
	}
	// if the file is a plan.md, extract the fenced summoner-task-graph block (M4)
	yamlBytes := extractGraphBlock(b)
	if yamlBytes == nil {
		yamlBytes = b
	}
	return graph.ParseGraph(yamlBytes)
}

// extractGraphBlock pulls the ```yaml summoner-task-graph fence from a markdown plan.
func extractGraphBlock(md []byte) []byte {
	re := regexp.MustCompile("(?s)```yaml\\s+summoner-task-graph\\s*\n(.*?)```")
	m := re.FindSubmatch(md)
	if len(m) < 2 {
		return nil
	}
	return m[1]
}

type fileTrace struct{ path string }

func (f *fileTrace) Append(e map[string]interface{}) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	fout, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fout.Close()
	_, err = fout.Write(append(b, '\n'))
	return err
}

func newWalker() (*graph.Walker, error) {
	g, err := loadGraph()
	if err != nil {
		return nil, err
	}
	var tr graph.TraceWriter
	if traceFile != "" {
		tr = &fileTrace{path: traceFile}
	} else {
		tr = &nullTrace{}
	}
	return graph.NewWalker(g, sessionID, tr), nil
}

type nullTrace struct{}

func (n *nullTrace) Append(e map[string]interface{}) error { return nil }

func nextCmd() *cobra.Command {
	return &cobra.Command{
		Use: "next", Short: "print the next directive",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			d := w.Next()
			out, _ := json.Marshal(d)
			fmt.Println(string(out))
			return nil
		},
	}
}

func recordCmd() *cobra.Command {
	var step, envelopePath, envelopeID, verdict, findingsPath, nodeID, evidencePath string
	c := &cobra.Command{
		Use: "record", Short: "record a handoff or review_verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			switch step {
			case "handoff":
				b, err := ioutil.ReadFile(envelopePath)
				if err != nil {
					return err
				}
				var env graph.HandoffEnvelope
				if err := json.Unmarshal(b, &env); err != nil {
					return err
				}
				d, err := w.RecordHandoff(env)
				if err != nil {
					return err
				}
				out, _ := json.Marshal(d)
				fmt.Println(string(out))
			case "review_verdict":
				var fs []graph.Finding
				if findingsPath != "" {
					b, err := ioutil.ReadFile(findingsPath)
					if err != nil {
						return err
					}
					if err := json.Unmarshal(b, &fs); err != nil {
						return err
					}
				}
				var ev []string
				if evidencePath != "" {
					b, err := ioutil.ReadFile(evidencePath)
					if err != nil {
						return err
					}
					if err := json.Unmarshal(b, &ev); err != nil {
						return err
					}
				}
				v := graph.ReviewVerdict{
					EnvelopeID:        envelopeID,
					Node:              nodeID,
					Reviewer:          "review-agent",
					Verdict:           verdict,
					Findings:          fs,
					EvidenceToolCalls: ev,
				}
				d, err := w.RecordReviewVerdict(v)
				if err != nil {
					return err
				}
				out, _ := json.Marshal(d)
				fmt.Println(string(out))
			default:
				return fmt.Errorf("unknown step %q (want handoff|review_verdict)", step)
			}
			return nil
		},
	}
	c.Flags().StringVar(&step, "step", "", "handoff | review_verdict")
	c.Flags().StringVar(&envelopePath, "envelope", "", "path to envelope json (handoff)")
	c.Flags().StringVar(&envelopeID, "envelope_id", "", "envelope id (review_verdict)")
	c.Flags().StringVar(&verdict, "verdict", "", "PASS | NEEDS-FIX (review_verdict)")
	c.Flags().StringVar(&findingsPath, "findings", "", "path to findings json (review_verdict)")
	c.Flags().StringVar(&nodeID, "node", "", "node id being reviewed (review_verdict)")
	c.Flags().StringVar(&evidencePath, "evidence", "", "path to evidence_tool_calls json array (review_verdict)")
	return c
}

func explainCmd() *cobra.Command {
	return &cobra.Command{
		Use: "explain", Short: "human-facing checkpoint render (M9)",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			fmt.Println(w.Explain())
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "raw machine state (debug/scorers)",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWalker()
			if err != nil {
				return err
			}
			fmt.Println(w.Status())
			return nil
		},
	}
}

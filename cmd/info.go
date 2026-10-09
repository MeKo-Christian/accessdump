package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/MeKo-Christian/accessdump/internal/mdb"
	"github.com/MeKo-Christian/accessdump/internal/vba"
	"github.com/spf13/cobra"
)

var (
	infoShowTree bool
	infoForensic bool
)

var infoCmd = &cobra.Command{
	Use:   "info [file]",
	Short: "Show MDB/ACCDB metadata and VBA project info",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := mdb.Open(args[0])
		if err != nil {
			return err
		}
		defer db.Close()

		out := cmd.OutOrStdout()

		names, _ := db.TableNames()
		sort.Strings(names)

		layout := layoutClass(db.PageSize(), db.Header.JetVersion)

		fmt.Fprintf(out, "file: %s\n", args[0])
		fmt.Fprintf(out, "jetVersion: %d\n", db.Header.JetVersion)
		fmt.Fprintf(out, "pageSize: %d\n", db.PageSize())
		fmt.Fprintf(out, "layoutClass: %s\n", layout)

		if hint := layoutHint(layout); hint != "" {
			fmt.Fprintf(out, "layoutHint: %s\n", hint)
		}

		fmt.Fprintf(out, "codepage: %d\n", db.Header.CodePage)

		cpErr := db.CodePageErr()
		if cpErr != nil {
			fmt.Fprintf(out, "codepageWarning: %s\n", cpErr)
		}

		fmt.Fprintf(out, "pageCount: %d\n", db.PageCount())
		fmt.Fprintf(out, "tableCount: %d\n", len(names))

		st, err := vba.LoadStorageTree(db)
		if err != nil {
			fmt.Fprintf(out, "vba: unavailable (%s)\n", formatCommandError(args[0], err))
			return nil
		}

		required, reqErr := st.RequiredStreams()
		if reqErr != nil {
			fmt.Fprintf(out, "vba: storage present, but required streams unresolved (%s)\n", formatCommandError(args[0], reqErr))
		} else {
			if projectNode := required["PROJECT"]; projectNode != nil && len(projectNode.Data) > 0 {
				project, parseErr := vba.ParseProjectStream(projectNode.Data)
				if parseErr == nil {
					fmt.Fprintf(out, "vbaProject: %s\n", project.Name)
					fmt.Fprintf(out, "vbaModules: %d\n", len(project.Modules))
				} else {
					fmt.Fprintf(out, "vbaProject: parse error (%v)\n", parseErr)
				}
			}
		}

		if infoShowTree {
			printStorageTree(out, st)
		}

		if infoForensic {
			printForensic(out, st)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(infoCmd)
	infoCmd.Flags().BoolVar(&infoShowTree, "tree", false, "Show storage tree")
	infoCmd.Flags().BoolVar(&infoForensic, "forensic", false, "Scan all storage streams for hidden/non-standard VBA candidates")
}

func printStorageTree(out io.Writer, st *vba.StorageTree) {
	root := st.Root()
	if root == nil {
		fmt.Fprintln(out, "storageTree: <no root>")
		return
	}

	fmt.Fprintln(out, "storageTree:")

	visited := map[int32]bool{}
	var walk func(node *vba.StorageNode, depth int)
	walk = func(node *vba.StorageNode, depth int) {
		if node == nil {
			return
		}

		if depth > 20 {
			indent := ""

			var indentSb92 strings.Builder
			for range depth {
				indentSb92.WriteString("  ")
			}

			indent += indentSb92.String()

			fmt.Fprintf(out, "%s- ... (depth limit reached)\n", indent)

			return
		}

		if visited[node.ID] {
			indent := ""

			var indentSb100 strings.Builder
			for range depth {
				indentSb100.WriteString("  ")
			}

			indent += indentSb100.String()

			fmt.Fprintf(out, "%s- %s (id=%d) [cycle]\n", indent, node.Name, node.ID)

			return
		}

		visited[node.ID] = true

		indent := ""

		var indentSb109 strings.Builder
		for range depth {
			indentSb109.WriteString("  ")
		}

		indent += indentSb109.String()

		fmt.Fprintf(out, "%s- %s (id=%d type=%d data=%d)\n", indent, node.Name, node.ID, node.Type, len(node.Data))

		for _, child := range st.Children[node.ID] {
			if child != nil && child.ID == node.ID {
				continue
			}

			walk(child, depth+1)
		}

		visited[node.ID] = false
	}

	walk(root, 0)
}

func printForensic(out io.Writer, st *vba.StorageTree) {
	report := vba.ForensicScanStorage(st)

	fmt.Fprintln(out, "forensic:")
	fmt.Fprintf(out, "  hits=%d projectCandidates=%d dirCandidates=%d sourceCandidates=%d compressedCandidates=%d artifactCandidates=%d\n",
		len(report.Hits), report.ProjectCandidates, report.DirCandidates, report.SourceCandidates, report.CompressedCandidates, report.ArtifactCandidates)

	limit := min(len(report.Hits), 25)

	for i := range limit {
		h := report.Hits[i]
		fmt.Fprintf(out, "  - id=%d name=%q type=%d size=%d kind=%s score=%d :: %s\n",
			h.NodeID, h.NodeName, h.NodeType, h.DataSize, h.Kind, h.Score, h.Summary)
	}

	if len(report.Hits) > limit {
		fmt.Fprintf(out, "  ... %d more hit(s)\n", len(report.Hits)-limit)
	}
}

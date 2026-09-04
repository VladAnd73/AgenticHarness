package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WrittenItem is one packet that actually landed in the harness.
type WrittenItem struct {
	N      int
	Claim  string
	Tier   string
	Target string
}

// RefusedItem is one packet the reviewer refuted or could not evidence.
type RefusedItem struct {
	N       int
	Claim   string
	Verdict string
	Reason  string
}

// HeldItem is a confirmed packet that cleared review but arrived after
// max_writes_per_run was already spent. It is not written and its
// ledger entry is left exactly as Gate left it, so it can be written on
// a later run without re-earning the two-tier bar.
type HeldItem struct {
	N      int
	Claim  string
	Tier   string
	Target string
}

// WriteReport is everything one run's write stage did, in the shape
// report.md renders.
type WriteReport struct {
	Written        []WrittenItem
	Refused        []RefusedItem
	Held           []HeldItem
	SkillProposals []string
}

// WriteRun is stages 4 and 5 combined: it records every reviewer
// verdict against the ledger, then writes the confirmed survivors,
// snapshotting every target once before touching any of them and
// sealing the run when it is done. A packet with no verdict file was
// held at the gate and never reached a reviewer, so it is skipped
// without comment: it is neither written nor refused, just still a
// candidate.
//
// maxWrites bounds how many confirmed packets are actually written.
// The rest are held, not discarded: their ledger entries stay
// candidate, exactly as Gate left them, so a later run can write them
// without re-earning the two-tier bar.
func WriteRun(project, runID, runDir string, maxWrites int) (WriteReport, error) {
	var report WriteReport
	packets, err := LoadPackets(runDir)
	if err != nil {
		return report, err
	}
	if len(packets) == 0 {
		if err := writeReportFile(runDir, report); err != nil {
			return report, err
		}
		return report, nil
	}

	l, err := LoadLedger(project)
	if err != nil {
		return report, err
	}

	var confirmed []PacketFile
	for _, pf := range packets {
		v, ok, err := LoadVerdict(runDir, pf.N)
		if err != nil {
			return report, err
		}
		if !ok {
			continue
		}
		fp := Fingerprint(pf.Packet.Type, pf.Packet.Claim)
		switch v.Verdict {
		case "confirmed":
			confirmed = append(confirmed, pf)
		case "refuted":
			l.Record(fp, StatusRefuted, v.Reason, runID)
			report.Refused = append(report.Refused, RefusedItem{pf.N, pf.Packet.Claim, v.Verdict, v.Reason})
		case "unevidenced":
			l.Record(fp, StatusUnevidenced, v.Reason, runID)
			report.Refused = append(report.Refused, RefusedItem{pf.N, pf.Packet.Claim, v.Verdict, v.Reason})
		default:
			return report, fmt.Errorf("dream: write: packet %d: unknown verdict %q", pf.N, v.Verdict)
		}
	}

	toWrite := confirmed
	if maxWrites < 0 {
		maxWrites = 0
	}
	if len(toWrite) > maxWrites {
		held := toWrite[maxWrites:]
		toWrite = toWrite[:maxWrites]
		for _, pf := range held {
			report.Held = append(report.Held, HeldItem{pf.N, pf.Packet.Claim, pf.Packet.Tier, pf.Packet.Target})
		}
	}

	targets, err := snapshotTargets(runDir, toWrite)
	if err != nil {
		return report, err
	}

	for _, pf := range toWrite {
		if err := writeOne(runDir, pf, &report); err != nil {
			return report, err
		}
		fp := Fingerprint(pf.Packet.Type, pf.Packet.Claim)
		l.Record(fp, StatusWritten, "", runID)
		report.Written = append(report.Written, WrittenItem{pf.N, pf.Packet.Claim, pf.Packet.Tier, pf.Packet.Target})
	}

	if len(targets) > 0 {
		if err := Seal(runDir); err != nil {
			return report, err
		}
	}
	if err := l.Save(); err != nil {
		return report, err
	}
	if err := writeReportFile(runDir, report); err != nil {
		return report, err
	}
	return report, nil
}

// snapshotTargets backs up every distinct non-skill target this batch
// will write, in one call: Snapshot replaces its manifest's file list
// on every call, so calling it once per packet would silently drop the
// backup of every packet but the last.
func snapshotTargets(runDir string, toWrite []PacketFile) ([]string, error) {
	var targets []string
	seen := map[string]bool{}
	for _, pf := range toWrite {
		if pf.Packet.Tier == "skill" {
			continue
		}
		if !filepath.IsAbs(pf.Packet.Target) {
			return nil, fmt.Errorf("dream: write: packet %d: target %q must be an absolute path",
				pf.N, pf.Packet.Target)
		}
		if !seen[pf.Packet.Target] {
			seen[pf.Packet.Target] = true
			targets = append(targets, pf.Packet.Target)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if err := Snapshot(runDir, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func writeOne(runDir string, pf PacketFile, report *WriteReport) error {
	switch pf.Packet.Tier {
	case "lesson":
		return writeLessonBlock(pf.Packet.Target, pf.Packet.Text)
	case "memory":
		return writeMemoryEntry(pf.Packet.Target, pf.Packet.Text)
	case "skill":
		path, err := writeSkillProposal(runDir, pf.Packet.Target, pf.Packet.Text)
		if err != nil {
			return err
		}
		report.SkillProposals = append(report.SkillProposals, path)
		return nil
	default:
		return fmt.Errorf("dream: write: packet %d: unknown tier %q", pf.N, pf.Packet.Tier)
	}
}

// writeLessonBlock appends text to target, the state-debt convention:
// any H2/H3 heading naming CRITICAL LESSON, RULE, or <word> SELF-LESSON
// is a lesson block, wherever in the file it lands. A missing state.md
// is not an error: it is created holding only this block.
func writeLessonBlock(target, text string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(target, []byte(text), 0o644)
	}
	body := strings.TrimRight(string(existing), "\n") + "\n\n" + strings.TrimRight(text, "\n") + "\n"
	return os.WriteFile(target, []byte(body), 0o644)
}

var (
	memoryNameRE        = regexp.MustCompile(`(?m)^name:\s*(.+)$`)
	memoryDescriptionRE = regexp.MustCompile(`(?m)^description:\s*(.+)$`)
)

// writeMemoryEntry writes text verbatim to target (a full memory file,
// frontmatter included) and appends its index line to MEMORY.md next to
// it. Both files are created if this is the first entry in a project
// that never had a memory tree.
func writeMemoryEntry(target, text string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(text), 0o644); err != nil {
		return err
	}
	return appendMemoryIndex(dir, filepath.Base(target), text)
}

func appendMemoryIndex(dir, filename, text string) error {
	title := filename
	if m := memoryNameRE.FindStringSubmatch(text); len(m) == 2 {
		title = titleCase(strings.TrimSpace(m[1]))
	}
	hook := "see file"
	if m := memoryDescriptionRE.FindStringSubmatch(text); len(m) == 2 {
		hook = strings.TrimSpace(m[1])
	}
	line := fmt.Sprintf("- [%s](%s) -- %s\n", title, filename, hook)

	indexPath := filepath.Join(dir, "MEMORY.md")
	existing, err := os.ReadFile(indexPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(indexPath, []byte("# project memory\n\n"+line), 0o644)
	}
	body := strings.TrimRight(string(existing), "\n") + "\n" + line
	return os.WriteFile(indexPath, []byte(body), 0o644)
}

func titleCase(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

var skillNameRE = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// writeSkillProposal always lands under runDir/skill-proposals/,
// whatever target names as the skill's eventual install path: skills
// are proposed, never installed, and the run directory is the only
// place this run is ever allowed to write one.
func writeSkillProposal(runDir, target, text string) (string, error) {
	dir := filepath.Join(runDir, "skill-proposals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(filepath.Dir(target))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	}
	name = strings.Trim(skillNameRE.ReplaceAllString(name, "-"), "-")
	if name == "" {
		name = "proposal"
	}
	path := filepath.Join(dir, name+".md")
	return path, os.WriteFile(path, []byte(text), 0o644)
}

func writeReportFile(runDir string, report WriteReport) error {
	var b strings.Builder
	b.WriteString("# Dream write report\n\n")
	fmt.Fprintf(&b, "written=%d refused=%d held=%d skill-proposals=%d\n\n",
		len(report.Written), len(report.Refused), len(report.Held), len(report.SkillProposals))

	b.WriteString("## Written\n\n")
	if len(report.Written) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, w := range report.Written {
			fmt.Fprintf(&b, "- [%s] %s -> %s: %s\n", w.Tier, w.Claim, w.Target, w.Claim)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Refused\n\n")
	if len(report.Refused) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, r := range report.Refused {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", r.Verdict, r.Claim, r.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Held as candidates (over max_writes_per_run)\n\n")
	if len(report.Held) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, h := range report.Held {
			fmt.Fprintf(&b, "- [%s] %s -> %s\n", h.Tier, h.Claim, h.Target)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Skill proposals awaiting review\n\n")
	if len(report.SkillProposals) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, p := range report.SkillProposals {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}

	return os.WriteFile(filepath.Join(runDir, "report.md"), []byte(b.String()), 0o644)
}

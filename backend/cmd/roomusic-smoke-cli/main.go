// Command roomusic-smoke-cli 用于比较 canonical Smoke snapshot。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	smoke "github.com/qlqq/roomusic/backend/cmd/roomusic-smoke"
)

const maxSnapshotBytes int64 = 512 << 20

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return errors.New("empty category")
		}
		*values = append(*values, item)
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return 0
	}
	if args[0] != "compare" {
		fmt.Fprintln(os.Stderr, "用法：roomusic-smoke-cli compare --expected FILE --actual FILE --output FILE [--fail-on-diff] [--fail-on-category CATEGORY]")
		return 2
	}

	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	expectedPath := flags.String("expected", "", "expected snapshot")
	actualPath := flags.String("actual", "", "actual snapshot")
	outputPath := flags.String("output", "", "comparison report")
	failOnDiff := flags.Bool("fail-on-diff", false, "exit 1 when any difference exists")
	var failCategories stringList
	flags.Var(&failCategories, "fail-on-category", "exit 1 when a difference has this category (repeatable or comma-separated)")
	if err := flags.Parse(args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "roomusic-smoke-cli: 参数无效：%v\n", err)
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "roomusic-smoke-cli: compare 不接受位置参数")
		return 2
	}
	if !absoluteRegularPath(*expectedPath) || !absoluteRegularPath(*actualPath) || !absoluteOutputPath(*outputPath) {
		fmt.Fprintln(os.Stderr, "roomusic-smoke-cli: 输入和输出路径必须是绝对路径且不含符号链接")
		return 2
	}

	expected, err := readSnapshot(*expectedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "roomusic-smoke-cli: expected snapshot 无效：%v\n", err)
		return 2
	}
	actual, err := readSnapshot(*actualPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "roomusic-smoke-cli: actual snapshot 无效：%v\n", err)
		return 2
	}
	report, compareErr := smoke.CompareStrict(expected, actual)
	if compareErr != nil {
		report.Errors = []string{compareErr.Error()}
	}
	encoded, err := report.JSON()
	if err != nil {
		fmt.Fprintf(os.Stderr, "roomusic-smoke-cli: 报告编码失败：%v\n", err)
		return 2
	}
	if err := writeReport(*outputPath, encoded); err != nil {
		fmt.Fprintf(os.Stderr, "roomusic-smoke-cli: 报告写入失败：%v\n", err)
		return 2
	}
	if compareErr != nil {
		return 2
	}
	if *failOnDiff && len(report.Differences) > 0 {
		return 1
	}
	if len(failCategories) > 0 {
		wanted := make(map[string]struct{}, len(failCategories))
		for _, category := range failCategories {
			wanted[category] = struct{}{}
		}
		for _, difference := range report.Differences {
			if _, ok := wanted[difference.Category]; ok {
				return 1
			}
		}
	}
	return 0
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "用法：roomusic-smoke-cli compare --expected FILE --actual FILE --output FILE [--fail-on-diff] [--fail-on-category CATEGORY]")
}

func absoluteRegularPath(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func absoluteOutputPath(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	if info, err := os.Lstat(path); err == nil {
		return info.Mode().IsRegular()
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

func readSnapshot(path string) (smoke.Snapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		return smoke.Snapshot{}, err
	}
	if info.Size() > maxSnapshotBytes {
		return smoke.Snapshot{}, fmt.Errorf("snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return smoke.Snapshot{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxSnapshotBytes+1))
	var snapshot smoke.Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return smoke.Snapshot{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return smoke.Snapshot{}, errors.New("snapshot contains multiple JSON values")
		}
		return smoke.Snapshot{}, err
	}
	if err := smoke.ValidateSnapshot(snapshot); err != nil {
		return smoke.Snapshot{}, err
	}
	return snapshot, nil
}

func writeReport(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if _, err := file.Write([]byte{'\n'}); err != nil {
		return err
	}
	return file.Chmod(0o600)
}

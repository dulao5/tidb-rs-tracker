package domains

// Domain "cgroup": the memory part of pkg/util/cgroup (block D15.3).
//
// The Go functions read files under a root directory ("/" in production). Each record
// carries a fixture, {"files": [[path, hex], ...], "dirs": [path, ...]}, that Exec (and
// the Rust replay) writes into a fresh temporary directory before the call; the paths
// are relative to that root (e.g. "/proc/self/cgroup"). The root appears in error
// messages (os.PathError texts, wrapped paths); both sides replace it with "<ROOT>".
//
// Fixture sources:
//   - the /proc files and value files of pkg/util/cgroup's tests (cmd/minelits -decls),
//     in the scenarios of TestCgroupsGetMemoryUsage / ...InactiveFileUsage / ...Limit and
//     the (cgroup, mountinfo) pairs of TestCgroupsGetCPU;
//   - those /proc files crossed with each other and with generated value files;
//   - generated /proc/self/cgroup and /proc/self/mountinfo files (v1, v2, hybrid, nested
//     and `:`-containing paths, optional fields, missing separators, Unicode spaces, CRLF,
//     over-long lines) with value files placed where the Go code looks (or missing,
//     misplaced, replaced by a directory, or with odd contents).

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/util/cgroup"
)

const cgroupSeed = 1503

//go:embed testdata/cgroup_literals.tsv
var cgroupLiteralsTSV string

//go:linkname cgGetMemLimit github.com/pingcap/tidb/pkg/util/cgroup.getCgroupMemLimit
func cgGetMemLimit(root string) (uint64, cgroup.Version, error)

//go:linkname cgGetMemUsage github.com/pingcap/tidb/pkg/util/cgroup.getCgroupMemUsage
func cgGetMemUsage(root string) (uint64, error)

//go:linkname cgGetMemInactive github.com/pingcap/tidb/pkg/util/cgroup.getCgroupMemInactiveFileUsage
func cgGetMemInactive(root string) (uint64, error)

//go:linkname cgDetectControlPath github.com/pingcap/tidb/pkg/util/cgroup.detectControlPath
func cgDetectControlPath(cgroupFilePath string, controller string) (string, error)

//go:linkname cgGetDetails github.com/pingcap/tidb/pkg/util/cgroup.getCgroupDetails
func cgGetDetails(mountInfoPath string, cRoot string, controller string) ([]string, []int, error)

//go:linkname cgDetectVersion github.com/pingcap/tidb/pkg/util/cgroup.detectCgroupVersion
func cgDetectVersion(fields [][]byte, controller string) (int, bool)

//go:linkname cgControllerMatch github.com/pingcap/tidb/pkg/util/cgroup.controllerMatch
func cgControllerMatch(field string, controller string) bool

//go:linkname cgReadFile github.com/pingcap/tidb/pkg/util/cgroup.readFile
func cgReadFile(filepath string) ([]byte, error)

//go:linkname cgReadInt64 github.com/pingcap/tidb/pkg/util/cgroup.readInt64Value
func cgReadInt64(root, filename string, cgVersion int) (uint64, error)

//go:linkname cgMemStat github.com/pingcap/tidb/pkg/util/cgroup.detectMemStatValue
func cgMemStat(cRoot, filename, key string, cgVersion int) (uint64, error)

var cgroupMinedFuncs = []string{"TestCgroupsGetMemoryUsage", "TestCgroupsGetMemoryInactiveFileUsage",
	"TestCgroupsGetMemoryLimit", "TestCgroupsGetCPU"}

// CgroupMinedTests lists the mined test functions (govec_test.go minedGoTests).
func CgroupMinedTests() []string { return cgroupMinedFuncs }

func init() {
	vec.Register(&vec.Domain{
		Name:     "cgroup",
		Seed:     cgroupSeed,
		Coverage: cgroupCoverage,
		Blocks:   []string{"D15.3"},
		Exempt: map[string]string{
			"GetMemoryLimit":             "getCgroupMemLimit with root \"/\" (the host's /proc); the root variant is covered",
			"GetCgroupMemLimit":          "getCgroupMemLimit with root \"/\" (the host's /proc); the root variant is covered",
			"GetMemoryUsage":             "getCgroupMemUsage with root \"/\" (the host's /proc); the root variant is covered",
			"GetMemoryInactiveFileUsage": "getCgroupMemInactiveFileUsage with root \"/\" (the host's /proc); the root variant is covered",
			"pkg/util/cgroup/cgroup_cpu_linux.go:InContainer":     "reads the fixed /proc/self paths of the host; Rust unit tests mirror inContainer's checks",
			"pkg/util/cgroup/cgroup_cpu_unsupport.go:InContainer": "the non-Linux build's constant false; Rust unit test",
			"inContainer": "compares its path argument with the fixed /proc/self paths, so it cannot read a fixture; Rust unit tests mirror its checks",
		},
		Gzip:     true,
		Generate: cgroupGenerate,
		Exec:     cgroupExec,
	})
}

var cgroupCoverage = map[string]vec.Coverage{
	"mem_limit":        {Min: 1000, GoFuncs: []string{"getCgroupMemLimit", "detectMemLimitInV1", "detectMemLimitInV2", "detectMemStatValue", "readInt64Value", "detectControlPath", "getCgroupDetails", "detectCgroupVersion", "controllerMatch"}},
	"mem_usage":        {Min: 1000, GoFuncs: []string{"getCgroupMemUsage", "detectMemUsageInV1", "detectMemUsageInV2"}},
	"mem_inactive":     {Min: 1000, GoFuncs: []string{"getCgroupMemInactiveFileUsage", "detectMemInactiveFileUsageInV1", "detectMemInactiveFileUsageInV2"}},
	"control_path":     {Min: 300, GoFuncs: []string{"detectControlPath"}},
	"details":          {Min: 300, GoFuncs: []string{"getCgroupDetails"}},
	"cgroup_version":   {Min: 300, GoFuncs: []string{"detectCgroupVersion"}},
	"controller_match": {Min: 200, GoFuncs: []string{"controllerMatch"}},
	"read_file":        {Min: 5, GoFuncs: []string{"readFile"}},
	"read_int64":       {Min: 300, GoFuncs: []string{"readInt64Value"}},
	"mem_stat":         {Min: 300, GoFuncs: []string{"detectMemStatValue"}},
}

// cgFixture is the file tree written under the root of one call.
type cgFixture struct {
	Files [][2]string `json:"files"` // [path, hex contents]
	Dirs  []string    `json:"dirs"`
}

func (f *cgFixture) add(path, contents string) {
	f.Files = append(f.Files, [2]string{path, hex.EncodeToString([]byte(contents))})
}

func (f *cgFixture) clone() cgFixture {
	return cgFixture{Files: append([][2]string(nil), f.Files...), Dirs: append([]string(nil), f.Dirs...)}
}

// cgNest: the fixture root is this deep below the temporary directory, so that paths
// with ".." (a cgroup path is joined after the mount point) stay inside it.
const cgNest = "n/n/n/n/n/n"

// write creates the tree under a fresh directory (createFiles of cgroup_mock_test.go,
// plus directories) and returns (temporary directory, root). A file whose parent cannot
// be created (a file is in the way) is skipped; the replay skips it the same way.
func (f *cgFixture) write() (string, string) {
	tmp, err := os.MkdirTemp("", "govec-cgroup")
	if err != nil {
		panic(err)
	}
	root := filepath.Join(tmp, cgNest)
	if err := os.MkdirAll(root, 0o755); err != nil {
		panic(err)
	}
	for _, d := range f.Dirs {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	for _, fe := range f.Files {
		data, err := hex.DecodeString(fe[1])
		if err != nil {
			panic(err)
		}
		p := filepath.Join(root, fe[0])
		if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
			continue
		}
		_ = os.WriteFile(p, data, 0o644)
	}
	return tmp, root
}

// cgErr replaces the root in an error text with "<ROOT>", then the temporary directory
// (paths that went up from the root with "..") with "<TMP>".
func cgErr(err error, tmp, root string) error {
	if err == nil {
		return nil
	}
	m := strings.ReplaceAll(err.Error(), root, "<ROOT>")
	return errors.New(strings.ReplaceAll(m, tmp, "<TMP>"))
}

func cgroupExec(op string, args []json.RawMessage) (any, error) {
	withRoot := func(f func(root string) (any, error)) (any, error) {
		fx := arg[cgFixture](args, 0)
		tmp, root := fx.write()
		defer os.RemoveAll(tmp)
		out, err := f(root)
		return out, cgErr(err, tmp, root)
	}
	switch op {
	case "mem_limit":
		return withRoot(func(root string) (any, error) {
			limit, ver, err := cgGetMemLimit(root)
			if err != nil {
				// The version is only meaningful with err == nil (its callers ignore it otherwise).
				return limit, err
			}
			return map[string]any{"limit": limit, "version": int(ver)}, nil
		})
	case "mem_usage":
		return withRoot(func(root string) (any, error) { return cgGetMemUsage(root) })
	case "mem_inactive":
		return withRoot(func(root string) (any, error) { return cgGetMemInactive(root) })
	case "control_path":
		controller := arg[string](args, 1)
		return withRoot(func(root string) (any, error) {
			p, err := cgDetectControlPath(filepath.Join(root, "/proc/self/cgroup"), controller)
			return vec.GoString(p), err
		})
	case "details":
		cRoot, controller := arg[string](args, 1), arg[string](args, 2)
		return withRoot(func(root string) (any, error) {
			mount, ver, err := cgGetDetails(filepath.Join(root, "/proc/self/mountinfo"), cRoot, controller)
			if err != nil {
				return nil, err
			}
			ms := make([]vec.GoString, len(mount))
			for i, m := range mount {
				ms[i] = vec.GoString(m)
			}
			return map[string]any{"mount": ms, "ver": ver}, nil
		})
	case "cgroup_version":
		line, controller := arg[string](args, 0), arg[string](args, 1)
		b, err := hex.DecodeString(line)
		if err != nil {
			panic(err)
		}
		ver, found := cgDetectVersion(bytesFields(b), controller)
		return map[string]any{"ver": ver, "found": found}, nil
	case "controller_match":
		field, controller := arg[string](args, 0), arg[string](args, 1)
		return cgControllerMatch(field, controller), nil
	case "read_file":
		path := arg[string](args, 1)
		return withRoot(func(root string) (any, error) {
			b, err := cgReadFile(filepath.Join(root, path))
			return hex.EncodeToString(b), err
		})
	case "read_int64":
		dir, filename, ver := arg[string](args, 1), arg[string](args, 2), arg[int](args, 3)
		return withRoot(func(root string) (any, error) {
			return cgReadInt64(filepath.Join(root, dir), filename, ver)
		})
	case "mem_stat":
		dir, filename, key, ver := arg[string](args, 1), arg[string](args, 2), arg[string](args, 3), arg[int](args, 4)
		return withRoot(func(root string) (any, error) {
			return cgMemStat(filepath.Join(root, dir), filename, key, ver)
		})
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// bytesFields is bytes.Fields (as getCgroupDetails splits a mountinfo line).
func bytesFields(b []byte) [][]byte { return bytes.Fields(b) }

// ---------------------------------------------------------------- generator

func cgroupFixtures() map[string]string {
	m := map[string]string{}
	for _, l := range parseDatumLiterals(cgroupLiteralsTSV) {
		if strings.HasPrefix(l.fn, "decl:") && l.str != nil {
			m[strings.TrimPrefix(l.fn, "decl:")] = *l.str
		}
	}
	// TestCgroupsGetCPU's second round: "cpu,cpuacct" written as "cpuacct,cpu".
	for _, n := range []string{"v1CgroupWithCPUController", "v1CgroupWithCPUControllerNS", "v1CgroupWithCPUControllerNSMountRel", "v1CgroupWithCPUControllerNSMountRelRemount", "v1CgroupWithCPUControllerNS2"} {
		m[n+"Swapped"] = strings.ReplaceAll(m[n], "cpu,cpuacct", "cpuacct,cpu")
	}
	for _, n := range []string{"v1MountsWithCPUController", "v1MountsWithCPUControllerNS", "v1MountsWithCPUControllerNSMountRel", "v1MountsWithCPUControllerNSMountRelRemount", "v1MountsWithCPUControllerNS2"} {
		m[n+"Swapped"] = strings.ReplaceAll(m[n], "rw,cpu,cpuacct", "rw,cpuacct,cpu")
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

const v2LibpodScope = "/sys/fs/cgroup/machine.slice/libpod-f1c6b44c0d61f273952b8daecf154cee1be2d503b7e9184ebf7fcaf48e139810.scope"

// cgroupGoTests: the case tables of cgroup_mock_test.go (paths -> fixture names or
// literal contents), each run through the op of its test.
func cgroupGoTests(g *vec.Gen, fx map[string]string) {
	type tcase map[string]string // path -> "=name" (a fixture) or literal contents
	build := func(tc tcase) cgFixture {
		var f cgFixture
		keys := make([]string, 0, len(tc))
		for k := range tc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := tc[k]
			if strings.HasPrefix(v, "=") {
				v = fx[v[1:]]
			}
			f.add(k, v)
		}
		return f
	}
	common := []tcase{
		{},
		{"/proc/self/cgroup": "=v1CgroupWithoutMemoryController", "/proc/self/mountinfo": "=v1MountsWithoutMemController"},
		{"/proc/self/cgroup": "=v1CgroupWithMemoryController"},
		{"/proc/self/cgroup": "=v1CgroupWithMemoryController", "/proc/self/mountinfo": "=v1MountsWithoutMemController"},
	}
	usage := append(append([]tcase{}, common...),
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryController", "/proc/self/mountinfo": "=v1MountsWithMemController", "/sys/fs/cgroup/memory/memory.usage_in_bytes": "=v1MemoryUsageInBytes"},
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryControllerNS", "/proc/self/mountinfo": "=v1MountsWithMemControllerNS", "/sys/fs/cgroup/memory/cgroup_test/memory.usage_in_bytes": "=v1MemoryUsageInBytes"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.current": "unparsable\n"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.current": "276328448"},
	)
	for _, tc := range usage {
		g.Add("TestCgroupsGetMemoryUsage", "mem_usage", build(tc))
	}
	inactive := append(append([]tcase{}, common...),
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryController", "/proc/self/mountinfo": "=v1MountsWithMemController", "/sys/fs/cgroup/memory/memory.stat": "=v1MemoryStat"},
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryControllerNS", "/proc/self/mountinfo": "=v1MountsWithMemControllerNS", "/sys/fs/cgroup/memory/cgroup_test/memory.stat": "=v1MemoryStat"},
		tcase{"/proc/self/cgroup": "=v1CgroupWithEccentricMemoryController", "/proc/self/mountinfo": "=v1MountsWithEccentricMemController", "/sys/fs/cgroup/memory/memory.stat": "=v1MemoryStat"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.stat": "inactive_file unparsable\n"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.stat": "=v2MemoryStat"},
	)
	for _, tc := range inactive {
		g.Add("TestCgroupsGetMemoryInactiveFileUsage", "mem_inactive", build(tc))
	}
	limit := append(append([]tcase{}, common...),
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryController", "/proc/self/mountinfo": "=v1MountsWithMemController", "/sys/fs/cgroup/memory/memory.stat": "=v1MemoryStat"},
		tcase{"/proc/self/cgroup": "=v1CgroupWithMemoryControllerNS", "/proc/self/mountinfo": "=v1MountsWithMemControllerNS", "/sys/fs/cgroup/memory/cgroup_test/memory.stat": "=v1MemoryStat"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.max": "unparsable\n"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.max": "1073741824\n"},
		tcase{"/proc/self/cgroup": "=v2CgroupWithMemoryController", "/proc/self/mountinfo": "=v2Mounts", v2LibpodScope + "/memory.max": "max\n"},
		tcase{"/proc/self/cgroup": "=v1CgroupWithEccentricMemoryController", "/proc/self/mountinfo": "=v1MountsWithEccentricMemController", "/sys/fs/cgroup/memory/memory.stat": "=v1MemoryStat"},
	)
	for _, tc := range limit {
		g.Add("TestCgroupsGetMemoryLimit", "mem_limit", build(tc))
	}
	// TestCgroupsGetCPU's (cgroup, mountinfo) pairs, in both rounds, through the memory
	// path detection (the CPU readers are X): which controller is found and where.
	cpuPairs := [][2]string{
		{"v1CgroupWithMemoryController", "v1MountsWithCPUController"},
		{"v1CgroupWithoutCPUController", "v1MountsWithoutCPUController"},
		{"v1CgroupWithCPUController", "v1MountsWithCPUController"},
		{"v1CgroupWithCPUControllerNS", "v1MountsWithCPUControllerNS"},
		{"v1CgroupWithCPUControllerNSMountRel", "v1MountsWithCPUControllerNSMountRel"},
		{"v1CgroupWithCPUControllerNSMountRelRemount", "v1MountsWithCPUControllerNSMountRelRemount"},
		{"v1CgroupWithCPUControllerNS2", "v1MountsWithCPUControllerNS2"},
		{"MixCgroup", "MixMounts"},
	}
	for _, sw := range []string{"", "Swapped"} {
		for _, p := range cpuPairs {
			c, m := p[0], p[1]
			if _, ok := fx[c+sw]; ok {
				c += sw
			}
			if _, ok := fx[m+sw]; ok {
				m += sw
			}
			f := build(tcase{"/proc/self/cgroup": "=" + c, "/proc/self/mountinfo": "=" + m})
			for _, ctl := range []string{"cpu,cpuacct", "memory"} {
				g.Add("TestCgroupsGetCPU", "control_path", f, ctl)
			}
			g.Add("TestCgroupsGetCPU", "mem_limit", f)
		}
	}
}

// The value-file contents the generator draws from.
var cgNumberContents = []string{
	"0", "1", "1073741824", "276328448", "9223372036854775807", "9223372036854775808",
	"18446744073709551615", "18446744073709551616", "99999999999999999999x", "max", "max\n",
	" max ", "Max", "MAX", "maxx", "-1", "+1", "1_000", "0x10", "010", "1e9", "1.5", "",
	"\n", "\n123", "  123  ", "123\n456", "\t9\r\n", "9\r", "\u00a0123\u00a0", "\u2003456\u3000",
	"\u0085789", "12\x003", "\xff", "abc\xfe", "123 456", " ", "\r\n", "unparsable\n",
	"4611686018427387904\n", "8796093022208 \n",
}

var cgStatKeys = []string{"hierarchical_memory_limit", "total_inactive_file", "inactive_file", "cache", "rss", "anon", "hierarchical_memsw_limit", "Inactive_file", "inactive_file:"}

func cgRandNumber(rng *rand.Rand) string {
	switch rng.IntN(4) {
	case 0:
		return fmt.Sprint(rng.Uint64())
	case 1:
		return fmt.Sprint(rng.Uint64N(1 << 40))
	default:
		return cgNumberContents[rng.IntN(len(cgNumberContents))]
	}
}

func cgRandStat(rng *rand.Rand) string {
	var b strings.Builder
	n := rng.IntN(12)
	seps := []string{" ", "  ", "\t", "\u00a0", " \t ", "\u2003"}
	for range n {
		key := cgStatKeys[rng.IntN(len(cgStatKeys))]
		switch rng.IntN(10) {
		case 0:
			fmt.Fprintf(&b, "%s%s%s%s%s", key, seps[rng.IntN(len(seps))], cgRandNumber(rng), " ", "extra")
		case 1:
			b.WriteString(key)
		default:
			fmt.Fprintf(&b, "%s%s%s", key, seps[rng.IntN(len(seps))], cgRandNumber(rng))
		}
		if rng.IntN(8) == 0 {
			b.WriteString("\r")
		}
		b.WriteString("\n")
	}
	if rng.IntN(5) == 0 {
		b.WriteString(cgStatKeys[rng.IntN(3)] + " " + cgRandNumber(rng)) // no trailing newline
	}
	return b.String()
}

var cgPathParts = []string{"kubepods", "besteffort", "pod1bf924dd-3f6f", "machine.slice", "libpod-f1c6.scope",
	"system.slice", "containerd.service", "user.slice", "cgroup_test", "crdb_test", "a b", "x:y", "..", ".", "ü", "\u00a0sp"}

func cgRandPath(rng *rand.Rand) string {
	switch rng.IntN(8) {
	case 0:
		return "/"
	case 1:
		return ""
	}
	n := 1 + rng.IntN(4)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = cgPathParts[rng.IntN(len(cgPathParts))]
	}
	p := strings.Join(parts, "/")
	if rng.IntN(6) != 0 {
		p = "/" + p
	}
	if rng.IntN(8) == 0 {
		p += "/"
	}
	return p
}

var cgV1Controllers = []string{"memory", "cpu,cpuacct", "cpuacct,cpu", "name=systemd", "memory,blkio", "blkio,memory",
	"pids", "rw,memory", "memory2", "MEMORY", "net_cls,net_prio", "devices", "cpuset", "hugetlb", ",memory", "memory,"}

// cgRandCgroupFile: a /proc/self/cgroup file and the cgroup path the memory controller
// is likely to be found under (for placing value files).
func cgRandCgroupFile(rng *rand.Rand) string {
	var lines []string
	n := rng.IntN(8)
	for i := range n {
		switch rng.IntN(12) {
		case 0:
			lines = append(lines, "0::"+cgRandPath(rng))
		case 1:
			lines = append(lines, fmt.Sprintf("%d::%s", 1+rng.IntN(5), cgRandPath(rng)))
		case 2:
			lines = append(lines, "garbage")
		case 3:
			lines = append(lines, "")
		case 4:
			lines = append(lines, fmt.Sprintf("%d:%s", i, cgV1Controllers[rng.IntN(len(cgV1Controllers))]))
		default:
			lines = append(lines, fmt.Sprintf("%d:%s:%s", 12-i, cgV1Controllers[rng.IntN(len(cgV1Controllers))], cgRandPath(rng)))
		}
	}
	sep := "\n"
	if rng.IntN(10) == 0 {
		sep = "\r\n"
	}
	s := strings.Join(lines, sep)
	if rng.IntN(3) != 0 {
		s += sep
	}
	if rng.IntN(60) == 0 {
		s = "1:memory:/" + strings.Repeat("l", 65536+rng.IntN(3)-1) + "\n" + s
	}
	return s
}

var cgMountPoints = []string{"/sys/fs/cgroup/memory", "/sys/fs/cgroup", "/sys/fs/cgroup/memory,cpu", "/sys/fs/cgroup/unified",
	"/cgroup", "/sys/fs/cgroup/memory/sub", "/", "/sys/fs/cgroup/a b"}

var cgSuperOpts = []string{"rw,memory", "rw,cpu,cpuacct", "rw,cpuacct,cpu", "memory", "rw", "rw,nsdelegate,memory_recursiveprot", "rw,blkio,memory", "ro,memory,xattr"}

func cgRandMountInfo(rng *rand.Rand, cgroupPath string) string {
	var lines []string
	n := rng.IntN(8)
	seps := []string{" ", " ", " ", "  ", "\t", "\u00a0", "\u2003"}
	for range n {
		sep := seps[rng.IntN(len(seps))]
		var root string
		switch rng.IntN(7) {
		case 0:
			root = "/"
		case 1:
			root = cgroupPath
		case 2:
			if i := strings.LastIndex(cgroupPath, "/"); i > 0 {
				root = cgroupPath[:i]
			} else {
				root = "/"
			}
		case 3:
			root = "/.."
		case 4:
			root = "relative"
		default:
			root = cgRandPath(rng)
		}
		if root == "" {
			root = "/"
		}
		mp := cgMountPoints[rng.IntN(len(cgMountPoints))]
		f := []string{fmt.Sprint(20 + rng.IntN(2000)), fmt.Sprint(rng.IntN(100)), "0:" + fmt.Sprint(rng.IntN(200)), root, mp, "rw,nosuid,nodev,noexec,relatime"}
		for range rng.IntN(3) {
			f = append(f, []string{"shared:25", "master:5", "propagate_from:1", "unbindable"}[rng.IntN(4)])
		}
		switch rng.IntN(10) {
		case 0: // no separator
		case 1:
			f = append(f, "-", "cgroup") // too few fields after it
		default:
			f = append(f, "-")
			switch rng.IntN(6) {
			case 0, 1:
				f = append(f, "cgroup", "cgroup", cgSuperOpts[rng.IntN(len(cgSuperOpts))])
			case 2, 3:
				f = append(f, "cgroup2", "cgroup2", "rw,nsdelegate")
			case 4:
				f = append(f, "tmpfs", "tmpfs", "rw,mode=755")
			default:
				f = append(f, "overlay", "overlay", "rw,lowerdir=/a:/b")
			}
		}
		lines = append(lines, strings.Join(f, sep))
	}
	// Usually one well-formed memory mount (v1 or v2) too, at a random position.
	if rng.IntN(10) < 7 {
		var l string
		if rng.IntN(2) == 0 {
			l = fmt.Sprintf("50 35 0:44 %s %s rw,nosuid shared:25 - cgroup cgroup %s", []string{"/", cgroupPath, "/"}[rng.IntN(3)],
				[]string{"/sys/fs/cgroup/memory", "/sys/fs/cgroup/memory,blkio"}[rng.IntN(2)], []string{"rw,memory", "rw,blkio,memory"}[rng.IntN(2)])
			if strings.Contains(l, "  ") { // an empty cgroupPath
				l = strings.Replace(l, "  ", " / ", 1)
			}
		} else {
			l = "30 23 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:4 - cgroup2 cgroup2 rw,nsdelegate"
		}
		i := rng.IntN(len(lines) + 1)
		lines = append(lines[:i], append([]string{l}, lines[i:]...)...)
	}
	if rng.IntN(10) == 0 {
		lines = append(lines, "short line")
	}
	end := "\n"
	if rng.IntN(10) == 0 {
		end = "\r\n"
	}
	return strings.Join(lines, end) + end
}

// cgPlaceValues adds value files where the Go code will look, given the /proc files of f
// (found by running the Go path detection on them), with odd placements and contents.
func cgPlaceValues(rng *rand.Rand, f *cgFixture) {
	tmp, probe := f.write()
	defer os.RemoveAll(tmp)
	path, err := cgDetectControlPath(filepath.Join(probe, "/proc/self/cgroup"), "memory")
	var dirs []string
	if err == nil && path != "" {
		if mount, ver, err := cgGetDetails(filepath.Join(probe, "/proc/self/mountinfo"), path, "memory"); err == nil {
			for i, m := range mount {
				if ver[i] == 1 {
					dirs = append(dirs, filepath.Join("/", m))
				}
				dirs = append(dirs, filepath.Join("/", m, path))
			}
		}
	}
	if len(dirs) == 0 || rng.IntN(10) == 0 {
		dirs = append(dirs, "/sys/fs/cgroup/memory", "/sys/fs/cgroup")
	}
	for _, d := range dirs {
		for _, name := range []string{"memory.stat", "memory.max", "memory.current", "memory.usage_in_bytes"} {
			switch rng.IntN(10) {
			case 0, 1: // missing
			case 2:
				f.Dirs = append(f.Dirs, filepath.Join(d, name))
			default:
				if name == "memory.stat" {
					f.add(filepath.Join(d, name), cgRandStat(rng))
				} else {
					f.add(filepath.Join(d, name), cgRandNumber(rng))
				}
			}
		}
	}
}

func cgroupGenerate(g *vec.Gen, rng *rand.Rand) {
	fx := cgroupFixtures()
	cgroupGoTests(g, fx)

	names := sortedKeys(fx)
	var cgroups, mounts []string
	for _, n := range names {
		switch {
		case strings.Contains(n, "Cgroup"):
			cgroups = append(cgroups, n)
		case strings.Contains(n, "Mounts"):
			mounts = append(mounts, n)
		}
	}
	memOps := []string{"mem_limit", "mem_usage", "mem_inactive"}

	// The Go test fixtures crossed with each other and with generated value files.
	for _, c := range cgroups {
		for _, m := range mounts {
			var f cgFixture
			f.add("/proc/self/cgroup", fx[c])
			f.add("/proc/self/mountinfo", fx[m])
			g.Add("cross/"+c+"/"+m, "details", f, "/", "memory")
			for range 2 {
				vf := f.clone()
				cgPlaceValues(rng, &vf)
				for _, op := range memOps {
					g.Add("cross/"+c+"/"+m, op, vf)
				}
			}
		}
	}

	// Generated /proc files.
	for range 1500 {
		var f cgFixture
		cgFile := cgRandCgroupFile(rng)
		cgPath := "/"
		for _, l := range strings.Split(cgFile, "\n") {
			if p := strings.SplitN(l, ":", 3); len(p) == 3 && (p[1] == "" || strings.Contains(p[1], "memory")) {
				cgPath = p[2]
			}
		}
		switch rng.IntN(20) {
		case 0: // no /proc/self/cgroup
		case 1:
			f.Dirs = append(f.Dirs, "/proc/self/cgroup")
		case 2:
			f.add("/proc/self", "a file in the way")
		default:
			f.add("/proc/self/cgroup", cgFile)
		}
		switch rng.IntN(20) {
		case 0: // no mountinfo
		case 1:
			f.Dirs = append(f.Dirs, "/proc/self/mountinfo")
		default:
			f.add("/proc/self/mountinfo", cgRandMountInfo(rng, cgPath))
		}
		cgPlaceValues(rng, &f)
		g.Add("rand/fs", memOps[rng.IntN(3)], f)
		if rng.IntN(3) == 0 {
			g.Add("rand/fs", memOps[rng.IntN(3)], f)
		}
		if rng.IntN(4) == 0 {
			g.Add("rand/fs", "control_path", f, []string{"memory", "cpu,cpuacct", "cpuacct,cpu", "pids", ""}[rng.IntN(5)])
		}
		if rng.IntN(4) == 0 {
			g.Add("rand/fs", "details", f, cgRandPath(rng), []string{"memory", "cpu,cpuacct", "memory,blkio"}[rng.IntN(3)])
		}
	}

	// Single mountinfo lines through detectCgroupVersion.
	for range 600 {
		l := strings.TrimRight(cgRandMountInfo(rng, cgRandPath(rng)), "\r\n")
		if i := strings.IndexAny(l, "\r\n"); i >= 0 {
			l = l[:i]
		}
		g.Add("rand/line", "cgroup_version", hex.EncodeToString([]byte(l)), []string{"memory", "cpu,cpuacct", "cpuacct,cpu", "rw", ""}[rng.IntN(5)])
	}

	// controllerMatch.
	ctrls := append(append([]string{}, cgV1Controllers...), "cpu", "cpuacct", "", ",", "cpu,cpuacct,memory", "a,b,c", "memory,memory")
	for _, a := range ctrls {
		for _, b := range ctrls {
			g.Add("sys/match", "controller_match", a, b)
		}
	}

	// readInt64Value and detectMemStatValue on single files.
	for range 500 {
		var f cgFixture
		dir := []string{"/sys/fs/cgroup/memory", "/x", "/"}[rng.IntN(3)]
		name := []string{"memory.max", "memory.current", "memory.usage_in_bytes"}[rng.IntN(3)]
		switch rng.IntN(12) {
		case 0:
		case 1:
			f.Dirs = append(f.Dirs, filepath.Join(dir, name))
		default:
			f.add(filepath.Join(dir, name), cgRandNumber(rng))
		}
		g.Add("rand/value", "read_int64", f, dir, name, 1+rng.IntN(2))
		var s cgFixture
		switch rng.IntN(12) {
		case 0:
		case 1:
			s.Dirs = append(s.Dirs, filepath.Join(dir, "memory.stat"))
		default:
			s.add(filepath.Join(dir, "memory.stat"), cgRandStat(rng))
		}
		g.Add("rand/value", "mem_stat", s, dir, "memory.stat", cgStatKeys[rng.IntN(4)], 1+rng.IntN(2))
	}
	// Lines at the bufio.Scanner limit (64 KiB).
	for _, n := range []int{65534, 65535, 65536, 65537} {
		for _, nl := range []string{"", "\n", "\r\n"} {
			var f cgFixture
			f.add("/sys/fs/cgroup/memory.max", strings.Repeat("1", n)+nl)
			g.Add("sys/scanner", "read_int64", f, "/sys/fs/cgroup", "memory.max", 2)
			var s cgFixture
			s.add("/sys/fs/cgroup/memory.stat", "inactive_file "+strings.Repeat("2", n-14)+nl+"inactive_file 7\n")
			g.Add("sys/scanner", "mem_stat", s, "/sys/fs/cgroup", "memory.stat", "inactive_file", 2)
		}
	}
	// readFile.
	for _, c := range []string{"", "abc", "\xff\x00", strings.Repeat("z", 70000)} {
		var f cgFixture
		f.add("/a/file", c)
		g.Add("sys/read_file", "read_file", f, "/a/file")
	}
	var fd cgFixture
	fd.Dirs = append(fd.Dirs, "/a/dir")
	g.Add("sys/read_file", "read_file", fd, "/a/dir")
	g.Add("sys/read_file", "read_file", cgFixture{}, "/missing")
	var fn cgFixture
	fn.add("/a", "x")
	g.Add("sys/read_file", "read_file", fn, "/a/b")
}

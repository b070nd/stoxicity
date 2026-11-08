package readiness

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"time"
)

// BenchSchema names the version of the benchmark report.
const BenchSchema = "stoxicity.bench/1"

// Machine describes where a measurement was made. A number without it cannot be
// compared with another.
type Machine struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	GoVersion string `json:"go_version"`
}

// CurrentMachine describes the machine this process runs on.
func CurrentMachine() Machine {
	return Machine{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GoVersion: runtime.Version()}
}

// FixtureRef names the deterministic data a measurement ran on.
type FixtureRef struct {
	Name       string `json:"name"`
	Seed       int64  `json:"seed"`
	Securities int    `json:"securities"`
}

// Measurement is one timed operation: its samples are summarized, never averaged
// alone, and the target it is held to is printed beside it.
type Measurement struct {
	Name    string        `json:"name"`
	Samples int           `json:"samples"`
	P50     time.Duration `json:"p50_ns"`
	P95     time.Duration `json:"p95_ns"`
	Max     time.Duration `json:"max_ns"`
	Target  time.Duration `json:"target_p95_ns,omitempty"`
}

// Met reports whether the p95 is within the target; true when there is none.
func (m Measurement) Met() bool { return m.Target == 0 || m.P95 <= m.Target }

// BenchReport is the format every performance claim of the project uses.
type BenchReport struct {
	Schema   string        `json:"schema"`
	At       time.Time     `json:"at"`
	Machine  Machine       `json:"machine"`
	Fixture  FixtureRef    `json:"fixture"`
	Results  []Measurement `json:"results"`
	Provider string        `json:"provider_latency"` // always "excluded": no network is part of a measurement
}

// SummarizeSamples turns raw samples into a Measurement.
func SummarizeSamples(name string, target time.Duration, samples []time.Duration) (Measurement, error) {
	if len(samples) == 0 {
		return Measurement{}, fmt.Errorf("readiness: no samples for %q", name)
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	at := func(p float64) time.Duration {
		i := int(p*float64(len(s))+0.999999) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(s) {
			i = len(s) - 1
		}
		return s[i]
	}
	return Measurement{Name: name, Samples: len(s), P50: at(0.50), P95: at(0.95), Max: s[len(s)-1], Target: target}, nil
}

// Security is one row of the reference catalogue.
type Security struct {
	Ticker, Name, Exchange, Currency string
}

// ReferenceCatalogue generates n securities from a seed. The same seed gives the
// same rows on every machine, so a timing can be repeated on the same data. The
// rows are invented and say nothing about any real company.
func ReferenceCatalogue(seed int64, n int) []Security {
	rng := rand.New(rand.NewSource(seed))
	venues := []struct{ mic, cur string }{{"XNAS", "USD"}, {"XNYS", "USD"}, {"XETR", "EUR"}, {"XPAR", "EUR"}, {"XAMS", "EUR"}, {"XLON", "GBP"}}
	words := []string{"Alder", "Birch", "Cedar", "Dune", "Elm", "Fjord", "Glen", "Harbor", "Iris", "Juniper", "Kestrel", "Lumen", "Meridian", "Nova", "Orchard", "Pine"}
	out := make([]Security, n)
	for i := range out {
		v := venues[rng.Intn(len(venues))]
		w1, w2 := words[rng.Intn(len(words))], words[rng.Intn(len(words))]
		out[i] = Security{
			Ticker:   fmt.Sprintf("%c%c%c%d", 'A'+rng.Intn(26), 'A'+rng.Intn(26), 'A'+rng.Intn(26), i),
			Name:     fmt.Sprintf("%s %s Holdings %d", w1, w2, i),
			Exchange: v.mic, Currency: v.cur,
		}
	}
	return out
}

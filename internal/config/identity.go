package config

import (
	"encoding/json"
	"strconv"
)

// Identity returns bytes that differ whenever two configurations can
// give different analysis results: the JSON of every value plus the
// position of every key the source set (in table order). Positions are
// part of it because diagnostics point at them (library/unmapped's
// quick fix names where libraries: is), so a cache keyed on the values
// alone would replay old positions (#37).
func (c *Config) Identity() []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // plain values always encode
	}
	for _, k := range c.SetKeys() {
		p := c.Position(k)
		b = append(b, 0)
		b = append(b, k...)
		b = append(b, 0)
		b = strconv.AppendInt(b, int64(p.Line), 10)
		b = append(b, ':')
		b = strconv.AppendInt(b, int64(p.Col), 10)
	}
	return b
}

package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A polite client for the Wikimedia APIs.
//
// Every response is written to a disk cache keyed by request. A cached entry is
// never re-fetched, so a run that is interrupted resumes exactly where it left
// off and a re-run costs no requests at all. Requests are serial with a fixed
// delay, and carry a User-Agent identifying the project and a contact address,
// as the Wikimedia API etiquette guidelines require.
type client struct {
	http      *http.Client
	cacheDir  string
	userAgent string
	delay     time.Duration
	log       *slog.Logger

	fetched int
	cached  int
}

func newClient(cacheDir, userAgent string, delay time.Duration, log *slog.Logger) (*client, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &client{
		http:      &http.Client{Timeout: 60 * time.Second},
		cacheDir:  cacheDir,
		userAgent: userAgent,
		delay:     delay,
		log:       log,
	}, nil
}

// cachePath derives a stable filename for a request. The key is hashed because
// a batch of fifty article titles makes an unusable filename.
func (c *client) cachePath(kind, key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(c.cacheDir, kind+"_"+hex.EncodeToString(sum[:])+".json")
}

// get fetches a URL, returning the cached body when one exists.
func (c *client) get(ctx context.Context, kind, key, endpoint string) ([]byte, error) {
	path := c.cachePath(kind, key)
	if b, err := os.ReadFile(path); err == nil {
		c.cached++
		return b, nil
	}

	// Only sleep before a request that is actually going out.
	if c.fetched > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d: %s", endpoint, resp.StatusCode, truncate(string(body), 200))
	}
	c.fetched++

	// Write via a temporary file so an interrupted run cannot leave a
	// half-written cache entry that would later be trusted.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return nil, fmt.Errorf("write cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("commit cache: %w", err)
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// resolveArticles maps ESPNcricinfo player ids to English Wikipedia article
// titles, via Wikidata property P2697 (ESPNcricinfo player ID).
//
// Wikidata is the join table here and nothing more: it carries the identifier
// crosswalk reliably, but almost no cricketer on it has a handedness statement,
// so the attributes themselves come from the Wikipedia article.
func (c *client) resolveArticles(ctx context.Context, cricinfoIDs []string) (map[string]string, error) {
	const batch = 400
	out := map[string]string{}

	for i := 0; i < len(cricinfoIDs); i += batch {
		end := min(i+batch, len(cricinfoIDs))
		chunk := cricinfoIDs[i:end]

		var vals strings.Builder
		for _, id := range chunk {
			fmt.Fprintf(&vals, "%q ", id)
		}
		query := fmt.Sprintf(`SELECT ?cricinfo ?article WHERE {
  VALUES ?cricinfo { %s }
  ?item wdt:P2697 ?cricinfo.
  ?article schema:about ?item; schema:isPartOf <https://en.wikipedia.org/>.
}`, vals.String())

		endpoint := "https://query.wikidata.org/sparql?" + url.Values{
			"query":  {query},
			"format": {"json"},
		}.Encode()

		body, err := c.get(ctx, "wikidata", query, endpoint)
		if err != nil {
			return nil, fmt.Errorf("resolve articles: %w", err)
		}

		var res struct {
			Results struct {
				Bindings []struct {
					Cricinfo struct{ Value string } `json:"cricinfo"`
					Article  struct{ Value string } `json:"article"`
				} `json:"bindings"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("parse sparql response: %w", err)
		}
		for _, b := range res.Results.Bindings {
			title, err := url.PathUnescape(b.Article.Value[strings.LastIndexByte(b.Article.Value, '/')+1:])
			if err != nil {
				continue
			}
			out[b.Cricinfo.Value] = strings.ReplaceAll(title, "_", " ")
		}
		c.log.Info("resolved article titles", "batch", i/batch+1, "matched", len(out), "of", len(cricinfoIDs))
	}
	return out, nil
}

// fetchWikitext returns the raw wikitext of each article, batched fifty at a
// time as the MediaWiki API permits.
func (c *client) fetchWikitext(ctx context.Context, titles []string) (map[string]string, error) {
	const batch = 50
	out := make(map[string]string, len(titles))

	for i := 0; i < len(titles); i += batch {
		end := min(i+batch, len(titles))
		chunk := titles[i:end]
		key := strings.Join(chunk, "|")

		endpoint := "https://en.wikipedia.org/w/api.php?" + url.Values{
			"action":        {"query"},
			"prop":          {"revisions"},
			"rvprop":        {"content"},
			"rvslots":       {"main"},
			"titles":        {key},
			"format":        {"json"},
			"formatversion": {"2"},
		}.Encode()

		body, err := c.get(ctx, "wikitext", key, endpoint)
		if err != nil {
			return nil, fmt.Errorf("fetch wikitext: %w", err)
		}

		var res struct {
			Query struct {
				Pages []struct {
					Title     string `json:"title"`
					Missing   bool   `json:"missing"`
					Revisions []struct {
						Slots struct {
							Main struct {
								Content string `json:"content"`
							} `json:"main"`
						} `json:"slots"`
					} `json:"revisions"`
				} `json:"pages"`
				Normalized []struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"normalized"`
			} `json:"query"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("parse wikitext response: %w", err)
		}

		// MediaWiki may normalise a requested title; map results back to the
		// title we asked for so the caller's keys still resolve.
		back := map[string]string{}
		for _, n := range res.Query.Normalized {
			back[n.To] = n.From
		}
		for _, p := range res.Query.Pages {
			if p.Missing || len(p.Revisions) == 0 {
				continue
			}
			title := p.Title
			if orig, ok := back[title]; ok {
				title = orig
			}
			out[title] = p.Revisions[0].Slots.Main.Content
		}
		c.log.Info("fetched wikitext", "batch", i/batch+1, "of", (len(titles)+batch-1)/batch,
			"articles", len(out), "requests", c.fetched, "from_cache", c.cached)
	}
	return out, nil
}

// Infobox field extraction.
//
// The cricketer infobox spells handedness in "batting" and bowling type in
// "bowling". Both are free text written by many editors, so the value is taken
// verbatim and normalised separately, and the raw string is kept in the output
// so any normalisation decision can be audited later.

var (
	reRef      = regexp.MustCompile(`(?s)<ref[^>]*>.*?</ref>|<ref[^>]*/>`)
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reTemplate = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	reLink     = regexp.MustCompile(`\[\[(?:[^\]|]*\|)?([^\]]*)\]\]`)
	reTag      = regexp.MustCompile(`<[^>]+>`)
	reBold     = regexp.MustCompile(`'{2,}`)
	reSpace    = regexp.MustCompile(`\s+`)
)

// listTemplates wrap content that must be kept. An infobox field is routinely
// written as {{ubl|Right-arm medium|Right-arm off break}}, and deleting the
// template outright throws away the only value the field carries.
var listTemplates = map[string]bool{
	"ubl":                  true,
	"unbulleted list":      true,
	"plainlist":            true,
	"plain list":           true,
	"flatlist":             true,
	"hlist":                true,
	"nowrap":               true,
	"nobr":                 true,
	"br separated entries": true,
}

// expandTemplates unwraps formatting templates and drops the rest.
//
// Templates are resolved innermost first so that nesting a wrapper inside
// another still yields its content.
func expandTemplates(s string) string {
	for range 8 {
		out := reTemplate.ReplaceAllStringFunc(s, func(m string) string {
			parts := strings.Split(m[2:len(m)-2], "|")
			if !listTemplates[strings.ToLower(strings.TrimSpace(parts[0]))] {
				return " "
			}
			var args []string
			for _, p := range parts[1:] {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				// Drop named parameters such as class=nowrap, keeping values.
				if i := strings.IndexByte(p, '='); i > 0 && !strings.ContainsAny(p[:i], " ,[]()") {
					continue
				}
				args = append(args, p)
			}
			return strings.Join(args, ", ")
		})
		if out == s {
			break
		}
		s = out
	}
	return s
}

// infoboxField pulls one named field out of an infobox.
func infoboxField(wikitext, field string) string {
	re, err := regexp.Compile(`(?im)^\s*\|\s*` + regexp.QuoteMeta(field) + `\s*=\s*(.+?)\s*$`)
	if err != nil {
		return ""
	}
	m := re.FindStringSubmatch(wikitext)
	if m == nil {
		return ""
	}
	return cleanWikitext(m[1])
}

// cleanWikitext strips markup, leaving readable plain text.
//
// Wikilinks are resolved before templates are expanded, because a piped link
// such as [[Off spin|off break]] contains the same separator that delimits
// template arguments; splitting on it first would cut the link in half.
func cleanWikitext(s string) string {
	s = reComment.ReplaceAllString(s, "")
	s = reRef.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = expandTemplates(s)
	s = reTag.ReplaceAllString(s, " ")
	s = reBold.ReplaceAllString(s, "")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), ","))
}

package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	ErrUnmarshalFrontMatter = errors.New("error while unmarshaling front matter")
	ErrConvertMarkdown      = errors.New("error while converting markdown to HTML")
)

type FrontMatter struct {
	Title       string    `yaml:"title"`
	Date        time.Time `yaml:"date"`
	Description string    `yaml:"description"`
	Draft       bool      `yaml:"draft"`
	Layout      string    `yaml:"layout"`
	Tags        []string  `yaml:"tags"`
}

type Page struct {
	Title       string
	Description string
	Date        string
	RawDate     time.Time
	Content     template.HTML
	Slug        string
	URL         string
	OutputPath  string
	SourcePath  string
	Layout      string
	Tags        []string
}

type SiteConfig struct {
	SiteTitle       string
	SiteName        string
	SiteAuthor      string
	SiteDescription string
	Year            int
	NavPath         string
	Favicon         string
	OGImage         string
}

type PageData struct {
	Page
	SiteConfig
}

type IndexData struct {
	SiteConfig
	RecentPosts []Page
}

type YearGroup struct {
	Year  int
	Posts []Page
}

type BlogData struct {
	SiteConfig
	Years []YearGroup
}

type BlogPostData struct {
	Page
	SiteConfig
}

// addSectionNumbers prepends hierarchical numbers (1., 1.1, 1.1.1, etc.) to
// headings in the rendered HTML. h2 is the top level (1.), h3 is second (1.1), and so on.
func addSectionNumbers(htmlContent string) string {
	re := regexp.MustCompile(`(<h([2-6])([^>]*)>)`)
	counters := make([]int, 5) // index 0 = h2, 1 = h3, ..., 4 = h6

	result := re.ReplaceAllStringFunc(htmlContent, func(match string) string {
		sub := re.FindStringSubmatch(match)
		level, _ := strconv.Atoi(sub[2])
		idx := level - 2 // h2 -> 0, h3 -> 1, etc.

		// increment the current level
		counters[idx]++
		// reset all deeper levels
		for i := idx + 1; i < len(counters); i++ {
			counters[i] = 0
		}

		// build the number string, e.g. "1.2"
		var parts []string
		for i := 0; i <= idx; i++ {
			parts = append(parts, strconv.Itoa(counters[i]))
		}
		number := strings.Join(parts, ".")

		return sub[1] + number + ". "
	})
	return result
}

func parsePage(path string, md goldmark.Markdown) (*Page, *FrontMatter, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var fm FrontMatter
	parts := bytes.SplitN(content, []byte("---"), 3)
	if len(parts) < 3 {
		return nil, nil, fmt.Errorf("invalid frontmatter in %s", path)
	}

	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		return nil, nil, fmt.Errorf("%s %w", path, ErrUnmarshalFrontMatter)
	}

	if fm.Draft {
		return nil, &fm, nil
	}

	var buf bytes.Buffer
	if err := md.Convert(parts[2], &buf); err != nil {
		return nil, nil, fmt.Errorf("failed to convert %s: %w", path, ErrConvertMarkdown)
	}

	filename := filepath.Base(path)
	slug := strings.TrimSuffix(filename, ".md")
	slug = strings.ToLower(slug)
	slug = strings.ReplaceAll(slug, " ", "-")

	page := &Page{
		Title:       fm.Title,
		Description: fm.Description,
		Date:        fm.Date.Format("2006-01-02"),
		RawDate:     fm.Date,
		Content:     template.HTML(buf.String()),
		Slug:        slug,
		SourcePath:  path,
		Layout:      fm.Layout,
		Tags:        fm.Tags,
	}

	return page, &fm, nil
}

func copyDir(srcDir, dstDir string, filter func(string) bool) error {
	return filepath.Walk(srcDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, relPath)
		if info.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if filter != nil && !filter(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0o644)
	})
}

func main() {
	md := goldmark.New(
		goldmark.WithExtensions(extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	// Parse blog posts from content/blog/
	var blogPosts []Page
	blogDir := "content/blog"
	if _, err := os.Stat(blogDir); err == nil {
		err := filepath.Walk(blogDir, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			page, _, err := parsePage(path, md)
			if err != nil {
				return err
			}
			if page == nil {
				return nil // draft
			}
			numberedHTML := addSectionNumbers(string(page.Content))
			page.Content = template.HTML(numberedHTML)
			page.URL = "/blog/" + page.Slug
			page.OutputPath = filepath.Join("build", "blog", page.Slug, "index.html")
			blogPosts = append(blogPosts, *page)
			return nil
		})
		if err != nil {
			log.Fatalf("Error reading blog posts: %v", err)
		}
	}

	// Sort blog posts by date (newest first)
	sort.Slice(blogPosts, func(i, j int) bool {
		return blogPosts[i].RawDate.After(blogPosts[j].RawDate)
	})

	// Parse standalone pages (about, now, projects)
	var standalonePages []Page
	standaloneFiles := []string{"content/about.md", "content/now.md", "content/projects.md"}
	for _, path := range standaloneFiles {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		page, _, err := parsePage(path, md)
		if err != nil {
			log.Fatalf("Error parsing %s: %v", path, err)
		}
		if page == nil {
			continue
		}
		page.URL = "/" + page.Slug
		page.OutputPath = filepath.Join("build", page.Slug, "index.html")
		standalonePages = append(standalonePages, *page)
	}

	// Setup build directories
	dirs := []string{"build", "build/css", "build/blog"}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("Error creating %s: %v", dir, err)
		}
	}

	// Copy CSS
	cssFiles := []string{"style.css"}
	for _, cssFile := range cssFiles {
		src, err := os.ReadFile(filepath.Join("public/style", cssFile))
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		if err := os.WriteFile(filepath.Join("build/css", cssFile), src, 0o644); err != nil {
			log.Fatalf("Error: %v", err)
		}
	}

	// Copy images
	if _, err := os.Stat("public/images"); err == nil {
		if err := copyDir("public/images", "build/images", nil); err != nil {
			log.Fatalf("Error copying images: %v", err)
		}
	}

	// Copy favicon
	if content, err := os.ReadFile("public/favicon.ico"); err == nil {
		if err := os.WriteFile("build/favicon.ico", content, 0o644); err != nil {
			log.Fatalf("Error writing favicon: %v", err)
		}
	}


	// Copy fonts
	fontDirs := []string{"IoskeleyMono", "Inter"}
	for _, fontDir := range fontDirs {
		srcDir := filepath.Join("public/fonts", fontDir)
		dstDir := filepath.Join("build/fonts", fontDir)
		fontFilter := func(path string) bool {
			ext := filepath.Ext(path)
			return ext == ".ttf" || ext == ".woff2" || ext == ".woff" || ext == ".otf" || ext == ".eot" || ext == ".svg"
		}
		if err := copyDir(srcDir, dstDir, fontFilter); err != nil {
			log.Fatal(err)
		}
	}

	funcMap := template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("02 Jan 2006")
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseGlob("templates/*.html")
	if err != nil {
		log.Fatal(err)
	}

	siteConfig := SiteConfig{
		SiteTitle:       "Arpan Ghosh",
		SiteName:        "Arpan Ghosh",
		SiteAuthor:      "Arpan Ghosh",
		SiteDescription: "blog",
		Year:            time.Now().Year(),
		Favicon:         "/favicon.ico",
		OGImage:         "/images/preview.png",
	}

	// Render blog posts
	for _, post := range blogPosts {
		dir := filepath.Dir(post.OutputPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("Error: %v", err)
		}

		data := BlogPostData{
			Page:       post,
			SiteConfig: siteConfig,
		}
		data.NavPath = "/blog"

		var output bytes.Buffer
		if err := tmpl.ExecuteTemplate(&output, "post.html", data); err != nil {
			log.Fatalf("Error rendering %s: %v", post.Slug, err)
		}
		if err := os.WriteFile(post.OutputPath, output.Bytes(), 0o644); err != nil {
			log.Fatalf("Error: %v", err)
		}
	}

	// Render standalone pages
	for _, page := range standalonePages {
		dir := filepath.Dir(page.OutputPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("Error: %v", err)
		}

		data := PageData{
			Page:       page,
			SiteConfig: siteConfig,
		}
		data.NavPath = page.URL

		var output bytes.Buffer
		if err := tmpl.ExecuteTemplate(&output, "page.html", data); err != nil {
			log.Fatalf("Error rendering %s: %v", page.Slug, err)
		}
		if err := os.WriteFile(page.OutputPath, output.Bytes(), 0o644); err != nil {
			log.Fatalf("Error: %v", err)
		}
	}

	// Render blog index with yearly grouping
	yearMap := make(map[int][]Page)
	for _, post := range blogPosts {
		year := post.RawDate.Year()
		yearMap[year] = append(yearMap[year], post)
	}
	var years []YearGroup
	for year, posts := range yearMap {
		years = append(years, YearGroup{Year: year, Posts: posts})
	}
	sort.Slice(years, func(i, j int) bool {
		return years[i].Year > years[j].Year
	})

	blogData := BlogData{
		SiteConfig: siteConfig,
		Years:      years,
	}
	blogData.NavPath = "/blog"
	var blogOutput bytes.Buffer
	if err := tmpl.ExecuteTemplate(&blogOutput, "blog.html", blogData); err != nil {
		log.Fatalf("Error rendering blog index: %v", err)
	}
	if err := os.MkdirAll("build/blog", 0o755); err != nil {
		log.Fatalf("Error: %v", err)
	}
	if err := os.WriteFile("build/blog/index.html", blogOutput.Bytes(), 0o644); err != nil {
		log.Fatalf("Error: %v", err)
	}

	// Render homepage with 5 most recent posts
	recentPosts := blogPosts
	if len(recentPosts) > 5 {
		recentPosts = recentPosts[:5]
	}
	indexData := IndexData{
		SiteConfig:  siteConfig,
		RecentPosts: recentPosts,
	}
	var indexOutput bytes.Buffer
	if err := tmpl.ExecuteTemplate(&indexOutput, "index.html", indexData); err != nil {
		log.Fatalf("Error rendering index: %v", err)
	}
	if err := os.WriteFile("build/index.html", indexOutput.Bytes(), 0o644); err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Printf("Built %d blog posts, %d pages\n", len(blogPosts), len(standalonePages))
}

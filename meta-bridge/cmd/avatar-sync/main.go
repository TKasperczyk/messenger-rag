package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	dbPath     = flag.String("db", "messenger.db", "Path to SQLite database")
	outputDir  = flag.String("output", "../web/static/avatars", "Output directory for avatars")
	concurrent = flag.Int("concurrent", 10, "Number of concurrent downloads")
	forceAll   = flag.Bool("force", false, "Re-download all avatars even if they exist")
)

type Contact struct {
	ID         int64
	Name       string
	PictureURL sql.NullString
}

func main() {
	flag.Parse()

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	// Open database
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get all contacts with profile picture URLs
	rows, err := db.Query("SELECT id, name, profile_picture_url FROM contacts WHERE id > 0 AND profile_picture_url IS NOT NULL AND profile_picture_url != ''")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query contacts: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.PictureURL); err != nil {
			continue
		}
		contacts = append(contacts, c)
	}

	fmt.Printf("Found %d contacts with profile picture URLs\n", len(contacts))

	// Download avatars concurrently
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, *concurrent)

	downloaded := 0
	skipped := 0
	failed := 0
	var mu sync.Mutex

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Follow redirects
		},
	}

	for _, contact := range contacts {
		wg.Add(1)
		go func(c Contact) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Determine file extension from URL
			ext := ".jpg"
			if strings.Contains(c.PictureURL.String, ".png") {
				ext = ".png"
			}
			filename := filepath.Join(*outputDir, fmt.Sprintf("%d%s", c.ID, ext))

			// Skip if already exists and not forcing
			if !*forceAll {
				// Check for any existing file with this ID
				jpgExists := false
				pngExists := false
				if _, err := os.Stat(filepath.Join(*outputDir, fmt.Sprintf("%d.jpg", c.ID))); err == nil {
					jpgExists = true
				}
				if _, err := os.Stat(filepath.Join(*outputDir, fmt.Sprintf("%d.png", c.ID))); err == nil {
					pngExists = true
				}
				if jpgExists || pngExists {
					mu.Lock()
					skipped++
					mu.Unlock()
					return
				}
			}

			// Download from the CDN URL in the database
			resp, err := client.Get(c.PictureURL.String)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			// Check content type - skip if not an image
			contentType := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(contentType, "image/") {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			// Adjust extension based on actual content type
			if strings.Contains(contentType, "png") {
				filename = filepath.Join(*outputDir, fmt.Sprintf("%d.png", c.ID))
			} else {
				filename = filepath.Join(*outputDir, fmt.Sprintf("%d.jpg", c.ID))
			}

			// Save to file
			file, err := os.Create(filename)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			defer file.Close()

			_, err = io.Copy(file, resp.Body)
			if err != nil {
				os.Remove(filename)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			mu.Lock()
			downloaded++
			fmt.Printf("Downloaded: %s (%d)\n", c.Name, c.ID)
			mu.Unlock()
		}(contact)
	}

	wg.Wait()

	fmt.Printf("\nDone! Downloaded: %d, Skipped: %d, Failed: %d\n", downloaded, skipped, failed)
}

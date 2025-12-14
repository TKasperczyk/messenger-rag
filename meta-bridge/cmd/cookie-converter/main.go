package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Cookie represents a Messenger cookie in the expected format
type MessengerCookies struct {
	CUser    string `json:"c_user,omitempty"`
	Xs       string `json:"xs,omitempty"`
	Datr     string `json:"datr,omitempty"`
	Fr       string `json:"fr,omitempty"`
	Sb       string `json:"sb,omitempty"`
	Wd       string `json:"wd,omitempty"`
	Dpr      string `json:"dpr,omitempty"`
	Presence string `json:"presence,omitempty"`
	Platform string `json:"Platform"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: cookie-converter <cookies.txt> [output.json]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Converts Netscape cookie format to Messenger JSON format.")
		fmt.Fprintln(os.Stderr, "If output.json is not specified, prints to stdout.")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := ""
	if len(os.Args) > 2 {
		outputPath = os.Args[2]
	}

	file, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	// Map to store cookies - prefer messenger.com cookies over facebook.com
	cookieMap := make(map[string]string)
	messengerCookies := make(map[string]string)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse Netscape cookie format:
		// domain, hostOnly, path, secure, expiry, name, value
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}

		domain := parts[0]
		name := parts[5]
		value := parts[6]

		// Check if this is a Facebook or Messenger cookie
		isMessenger := strings.Contains(domain, "messenger.com")
		isFacebook := strings.Contains(domain, "facebook.com")

		if !isMessenger && !isFacebook {
			continue
		}

		// Prefer messenger.com cookies
		if isMessenger {
			messengerCookies[name] = value
		} else if _, exists := messengerCookies[name]; !exists {
			// Only use facebook.com cookie if we don't have a messenger.com one
			cookieMap[name] = value
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Merge messenger cookies (they take priority)
	for k, v := range messengerCookies {
		cookieMap[k] = v
	}

	// Build the output structure
	cookies := MessengerCookies{
		CUser:    cookieMap["c_user"],
		Xs:       cookieMap["xs"],
		Datr:     cookieMap["datr"],
		Fr:       cookieMap["fr"],
		Sb:       cookieMap["sb"],
		Wd:       cookieMap["wd"],
		Dpr:      cookieMap["dpr"],
		Presence: cookieMap["presence"],
		Platform: "messenger",
	}

	// Check for required cookies
	if cookies.CUser == "" || cookies.Xs == "" {
		fmt.Fprintln(os.Stderr, "Warning: Missing required cookies (c_user or xs)")
		fmt.Fprintln(os.Stderr, "Make sure you're logged into messenger.com or facebook.com")
	}

	// Output JSON
	output, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating JSON: %v\n", err)
		os.Exit(1)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, output, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Cookies written to %s\n", outputPath)
		fmt.Fprintf(os.Stderr, "User ID: %s\n", cookies.CUser)
	} else {
		fmt.Println(string(output))
	}
}

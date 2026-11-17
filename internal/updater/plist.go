package updater

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
)

func mountPointFromPlist(plist []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(plist))
	var keys []string
	var lastKey string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "key":
			var k string
			if err := dec.DecodeElement(&k, &el); err == nil {
				lastKey = k
			}
		case "string":
			var v string
			if err := dec.DecodeElement(&v, &el); err == nil && lastKey == "mount-point" && v != "" {
				keys = append(keys, v)
			}
		}
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("hdiutil did not report a mount point")
	}
	return keys[len(keys)-1], nil
}

func bundleIDFromXML(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var lastKey string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("CFBundleIdentifier not found")
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "key":
			var k string
			if err := dec.DecodeElement(&k, &el); err == nil {
				lastKey = k
			}
		case "string":
			var v string
			if err := dec.DecodeElement(&v, &el); err == nil && lastKey == "CFBundleIdentifier" {
				if v == "" {
					return "", fmt.Errorf("empty bundle id")
				}
				return v, nil
			}
		}
	}
}

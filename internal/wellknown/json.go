package wellknown

import (
	"encoding/json"
	"fmt"
)

// MarshalIndex serializes a feed index with indentation.
func MarshalIndex(idx *FeedIndex) ([]byte, error) {
	if idx == nil {
		return nil, fmt.Errorf("nil feed index")
	}
	if idx.Version == 0 {
		idx.Version = 1
	}
	return json.MarshalIndent(idx, "", "  ")
}

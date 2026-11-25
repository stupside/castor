package hls

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type pngChunks struct {
	trailer   []byte
	parts     map[int]textPart
	length    uint32
	hasLength bool
}

type textPart struct {
	body       []byte
	compressed bool
}

// Walk real chunk boundaries so an IEND or iTXt string inside IDAT is never
// mistaken for metadata. Check checksums even on paths that skip pixel decoding.
func readPNGChunks(body []byte) (pngChunks, error) {
	var chunks pngChunks
	data := body[8:]
	hasImage := false
	for len(data) >= 12 {
		n := binary.BigEndian.Uint32(data)
		if uint64(n) > uint64(len(data)-12) {
			return chunks, fmt.Errorf("truncated PNG chunk")
		}
		end := 8 + int(n)
		if crc32.ChecksumIEEE(data[4:end]) != binary.BigEndian.Uint32(data[end:]) {
			return chunks, fmt.Errorf("invalid PNG chunk checksum")
		}
		content := data[8:end]
		switch string(data[4:8]) {
		case "IDAT":
			hasImage = true
		case "kwLn":
			if len(content) != 4 || chunks.hasLength {
				return chunks, fmt.Errorf("invalid kwLn metadata")
			}
			chunks.hasLength = true
			chunks.length = binary.BigEndian.Uint32(content)
		case "iTXt":
			if err := chunks.addText(content); err != nil {
				return chunks, err
			}
		case "IEND":
			if !hasImage || n != 0 {
				return chunks, fmt.Errorf("invalid PNG end")
			}
			chunks.trailer = data[end+4:]
			return chunks, nil
		}
		data = data[end+4:]
	}
	return chunks, fmt.Errorf("missing PNG end")
}

func (p *pngChunks) addText(content []byte) error {
	if !bytes.HasPrefix(content, []byte("payload-")) {
		return nil
	}
	name, fields, ok := bytes.Cut(content[len("payload-"):], []byte{0})
	// PNG keywords are at most 79 bytes. Bound both the ordinal and the
	// number of retained slices independently of the value a chunk declares.
	if len(name) == 0 || len(bytes.Trim(name, "0123456789")) != 0 {
		return nil // An unrelated, ordinary iTXt keyword.
	}
	if !ok || len(name) > 4 {
		return fmt.Errorf("invalid PNG payload part number")
	}
	index, err := strconv.Atoi(string(name))
	if err != nil || index >= 4096 {
		return fmt.Errorf("invalid PNG payload part number")
	}
	if len(fields) < 2 || fields[0] > 1 || fields[1] != 0 {
		return fmt.Errorf("invalid PNG payload compression")
	}
	_, translated, hasLanguage := bytes.Cut(fields[2:], []byte{0})
	_, text, hasTranslation := bytes.Cut(translated, []byte{0})
	if !hasLanguage || !hasTranslation {
		return fmt.Errorf("invalid PNG payload text fields")
	}
	if _, dup := p.parts[index]; dup {
		return fmt.Errorf("duplicate PNG payload part %d", index)
	}
	if p.parts == nil {
		p.parts = make(map[int]textPart)
	}
	p.parts[index] = textPart{body: text, compressed: fields[0] == 1}
	return nil
}

func (p pngChunks) textSegment() ([]byte, media.HLSExtraSegmentFormat, error) {
	var encoded bytes.Buffer
	limit := base64.StdEncoding.EncodedLen(pixelLimit)
	for index := range len(p.parts) {
		part, ok := p.parts[index]
		if !ok {
			return nil, 0, fmt.Errorf("missing PNG payload part %d", index)
		}
		remaining := limit - encoded.Len()
		if part.compressed {
			r, err := zlib.NewReader(bytes.NewReader(part.body))
			if err != nil {
				return nil, 0, err
			}
			_, err = io.Copy(&encoded, io.LimitReader(r, int64(remaining)+1))
			r.Close()
			if err != nil {
				return nil, 0, err
			}
		} else if len(part.body) <= remaining {
			encoded.Write(part.body)
		} else {
			return nil, 0, fmt.Errorf("PNG payload text exceeds size limit")
		}
		if encoded.Len() > limit {
			return nil, 0, fmt.Errorf("PNG payload text exceeds size limit")
		}
	}
	ts, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding.Strict(), &encoded))
	if err != nil {
		return nil, 0, fmt.Errorf("decoding PNG payload text: %w", err)
	}
	return checkedTS(ts, media.HLSExtraSegmentPNGText)
}

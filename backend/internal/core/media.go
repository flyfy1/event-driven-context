package core

import "encoding/binary"

// ValidateAudioContent applies the bounded container/type checks shared by
// authenticated upload surfaces. It is intentionally not a media decoder.
func ValidateAudioContent(mediaType string, data []byte) bool {
	switch mediaType {
	case "audio/mp4":
		return isAACMP4(data)
	case "audio/mpeg":
		return isMP3(data)
	case "audio/wav":
		return isWAV(data)
	default:
		return false
	}
}

type mp4Box struct {
	kind    string
	payload []byte
}

func isAACMP4(data []byte) bool {
	// This is a bounded container/type gate, not a decoder. The downstream
	// transcription runner remains responsible for proving the audio decodes.
	budget := 4096
	boxes, ok := parseMP4Boxes(data, &budget)
	if !ok {
		return false
	}
	hasFileType := false
	for _, box := range boxes {
		switch box.kind {
		case "ftyp":
			hasFileType = len(box.payload) >= 8
		case "moov":
			if hasAACTrack(box.payload, &budget) && hasFileType {
				return true
			}
		}
	}
	return false
}

func parseMP4Boxes(data []byte, budget *int) ([]mp4Box, bool) {
	boxes := []mp4Box{}
	for len(data) != 0 {
		if len(data) < 8 || *budget == 0 {
			return nil, false
		}
		*budget--
		headerSize := uint64(8)
		size := uint64(binary.BigEndian.Uint32(data[:4]))
		if size == 1 {
			if len(data) < 16 {
				return nil, false
			}
			headerSize = 16
			size = binary.BigEndian.Uint64(data[8:16])
		} else if size == 0 {
			size = uint64(len(data))
		}
		if size < headerSize || size > uint64(len(data)) {
			return nil, false
		}
		boxes = append(boxes, mp4Box{kind: string(data[4:8]), payload: data[headerSize:size]})
		data = data[size:]
	}
	return boxes, true
}

func hasAACTrack(moov []byte, budget *int) bool {
	boxes, ok := parseMP4Boxes(moov, budget)
	if !ok {
		return false
	}
	for _, box := range boxes {
		if box.kind == "trak" && trackHasAAC(box.payload, budget) {
			return true
		}
	}
	return false
}

func trackHasAAC(track []byte, budget *int) bool {
	boxes, ok := parseMP4Boxes(track, budget)
	if !ok {
		return false
	}
	for _, box := range boxes {
		if box.kind != "mdia" {
			continue
		}
		mediaBoxes, valid := parseMP4Boxes(box.payload, budget)
		if !valid {
			return false
		}
		isAudio := false
		var minf []byte
		for _, mediaBox := range mediaBoxes {
			switch mediaBox.kind {
			case "hdlr":
				isAudio = len(mediaBox.payload) >= 12 && string(mediaBox.payload[8:12]) == "soun"
			case "minf":
				minf = mediaBox.payload
			}
		}
		if isAudio && minfHasAAC(minf, budget) {
			return true
		}
	}
	return false
}

func minfHasAAC(minf []byte, budget *int) bool {
	boxes, ok := parseMP4Boxes(minf, budget)
	if !ok {
		return false
	}
	for _, box := range boxes {
		if box.kind != "stbl" {
			continue
		}
		tableBoxes, valid := parseMP4Boxes(box.payload, budget)
		if !valid {
			return false
		}
		for _, tableBox := range tableBoxes {
			if tableBox.kind == "stsd" && sampleDescriptionHasAAC(tableBox.payload, budget) {
				return true
			}
		}
	}
	return false
}

func sampleDescriptionHasAAC(data []byte, budget *int) bool {
	if len(data) < 8 {
		return false
	}
	entryCount := binary.BigEndian.Uint32(data[4:8])
	entries, ok := parseMP4Boxes(data[8:], budget)
	if !ok || uint64(entryCount) != uint64(len(entries)) {
		return false
	}
	for _, entry := range entries {
		if entry.kind != "mp4a" || len(entry.payload) < 28 {
			continue
		}
		dataReference := binary.BigEndian.Uint16(entry.payload[6:8])
		channels := binary.BigEndian.Uint16(entry.payload[16:18])
		sampleSize := binary.BigEndian.Uint16(entry.payload[18:20])
		sampleRate := binary.BigEndian.Uint32(entry.payload[24:28])
		if dataReference == 0 || channels == 0 || sampleSize == 0 || sampleRate == 0 {
			continue
		}
		children, valid := parseMP4Boxes(entry.payload[28:], budget)
		if valid && containsESDescriptor(children, budget, 0) {
			return true
		}
	}
	return false
}

func containsESDescriptor(boxes []mp4Box, budget *int, depth int) bool {
	if depth > 8 {
		return false
	}
	for _, box := range boxes {
		if box.kind == "esds" && len(box.payload) >= 4 {
			return true
		}
		if box.kind == "wave" {
			children, ok := parseMP4Boxes(box.payload, budget)
			if ok && containsESDescriptor(children, budget, depth+1) {
				return true
			}
		}
	}
	return false
}

func isMP3(data []byte) bool {
	start := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		for _, b := range data[6:10] {
			if b&0x80 != 0 {
				return false
			}
		}
		tagSize := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
		start = 10 + tagSize
		if start >= len(data) {
			return false
		}
	}
	end := min(len(data), start+(64<<10))
	for i := start; i+4 <= end; i++ {
		if data[i] == 0xff && data[i+1]&0xe0 == 0xe0 && data[i+1]&0x18 != 0x08 && data[i+1]&0x06 != 0 && data[i+2]&0xf0 != 0 && data[i+2]&0xf0 != 0xf0 && data[i+2]&0x0c != 0x0c {
			return true
		}
	}
	return false
}

func isWAV(data []byte) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 > uint64(len(data)) {
		return false
	}
	seenFormat, seenData := false, false
	for offset := 12; offset+8 <= len(data); {
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := uint64(offset + 8)
		end := start + size
		if end > uint64(len(data)) {
			return false
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			seenFormat = size >= 16
		case "data":
			seenData = size > 0
		}
		next := end + size%2
		if next > uint64(len(data)) {
			return false
		}
		offset = int(next)
	}
	return seenFormat && seenData
}

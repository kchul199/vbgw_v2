package ai

import (
	"bytes"
	"encoding/binary"
)

// AddWAVHeader는 Raw PCM 16kHz 16bit Mono 데이터를 Whisper가 인식 가능한 WAV 포맷으로 변환합니다.
func AddWAVHeader(pcmData []byte) []byte {
	size := len(pcmData)
	buf := new(bytes.Buffer)

	// RIFF 헤더
	buf.Write([]byte("RIFF"))
	binary.Write(buf, binary.LittleEndian, uint32(36+size))
	buf.Write([]byte("WAVE"))

	// fmt 청크
	buf.Write([]byte("fmt "))
	binary.Write(buf, binary.LittleEndian, uint32(16))          // Subchunk1Size
	binary.Write(buf, binary.LittleEndian, uint16(1))           // AudioFormat (1 = PCM)
	binary.Write(buf, binary.LittleEndian, uint16(1))           // NumChannels (1 = Mono)
	binary.Write(buf, binary.LittleEndian, uint32(16000))       // SampleRate (16kHz)
	binary.Write(buf, binary.LittleEndian, uint32(16000*2))     // ByteRate (SampleRate * NumChannels * BitsPerSample/8)
	binary.Write(buf, binary.LittleEndian, uint16(2))           // BlockAlign (NumChannels * BitsPerSample/8)
	binary.Write(buf, binary.LittleEndian, uint16(16))          // BitsPerSample

	// data 청크
	buf.Write([]byte("data"))
	binary.Write(buf, binary.LittleEndian, uint32(size))
	buf.Write(pcmData)

	return buf.Bytes()
}

// Resample24To16: OpenAI 24kHz PCM 데이터를 브릿지용 16kHz PCM으로 변환 (3:2 Downsampling with Linear Interpolation)
func Resample24To16(input []byte) []byte {
	if len(input) < 4 {
		return input
	}

	// 16-bit PCM (2 bytes per sample)
	inSamples := len(input) / 2
	outSamples := (inSamples * 2) / 3
	output := make([]byte, outSamples*2)

	for i := 0; i < outSamples; i++ {
		// Calculate position in the input array (ratio 1.5)
		pos := float64(i) * 1.5
		idx := int(pos)
		frac := pos - float64(idx)

		if idx+1 >= inSamples {
			// Last sample
			output[i*2] = input[idx*2]
			output[i*2+1] = input[idx*2+1]
			continue
		}

		// Get two adjacent samples for interpolation
		s1 := int16(binary.LittleEndian.Uint16(input[idx*2 : idx*2+2]))
		s2 := int16(binary.LittleEndian.Uint16(input[(idx+1)*2 : (idx+1)*2+2]))

		// Linear interpolation: s1 * (1-frac) + s2 * frac
		res := int16(float64(s1)*(1.0-frac) + float64(s2)*frac)
		binary.LittleEndian.PutUint16(output[i*2:i*2+2], uint16(res))
	}

	return output
}


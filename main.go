/*
MIT License

Copyright (c) 2025 Woyo

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"context"
	"log"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

// --- 設定區結束 ---
type Config struct {
	LanguageCode   string  `yaml:"languageCode"`
	VoiceName      string  `yaml:"voiceName"`
	InputFilename  string  `yaml:"inputFilename"`
	OutputFilename string  `yaml:"outputFilename"`
	MaxInputBytes  int     `yaml:"maxInputBytes"`
	SpeakingRate   float64 `yaml:"speakingRate"`
	Pitch          float64 `yaml:"pitch"`
}

// splitText 將長文本分割成不超過 maxSize bytes 的片段
func splitText(text string, maxSize int) []string {
	var chunks []string
	//var currentChunk bytes.Buffer
	//var currentSize int

	// 輔助函數：嘗試在最後幾個字元中尋找斷句符號
	findSplitPoint := func(s string) int {
		lookBack := 100 // 往回找 100 個 rune
		if len(s) < lookBack {
			lookBack = len(s)
		}
		sub := s[len(s)-lookBack:]
		bestSplit := -1
		// 優先找換行或主要標點
		for _, punc := range []string{"\n", "。", "！", "？", ".", "!", "?"} {
			if idx := strings.LastIndex(sub, punc); idx != -1 {
				// 確保標點後還有內容，或者標點就是結尾
				// runeIdx := utf8.RuneCountInString(sub[:idx+len(punc)])
				byteIdx := len(s) - lookBack + idx + len(punc)
				// 檢查是否真的接近結尾
				if len(s)-byteIdx < 150 || bestSplit == -1 { // 如果接近結尾或還沒找到更好的點
					bestSplit = byteIdx
				}
			}
		}
		// 如果找不到主要標點，找逗號或分號
		if bestSplit == -1 {
			for _, punc := range []string{"，", "；", ",", ";"} {
				if idx := strings.LastIndex(sub, punc); idx != -1 {
					byteIdx := len(s) - lookBack + idx + len(punc)
					if len(s)-byteIdx < 150 || bestSplit == -1 {
						bestSplit = byteIdx
					}
				}
			}
		}

		if bestSplit != -1 {
			return bestSplit
		}
		// 如果都找不到，就直接在 maxSize 附近切
		return len(s)
	}

	tempBytes := []byte(text)
	startIndex := 0

	for startIndex < len(tempBytes) {
		endIndex := startIndex + maxSize
		if endIndex >= len(tempBytes) {
			endIndex = len(tempBytes)
		} else {
			// 嘗試找到一個好的斷點，避免切斷 UTF-8 字元
			// 從 maxSize 往前找，直到找到一個 rune 的邊界
			for endIndex > startIndex && !utf8.RuneStart(tempBytes[endIndex]) {
				endIndex--
			}
			// 如果 endIndex 回到了 startIndex，表示單一 rune 就超過 maxSize (不太可能)
			// 或者 maxSize 太小，無法容納任何字元
			if endIndex <= startIndex {
				// 強制推進至少一個 rune，或者取 maxSize，以防死循環
				_, runeSize := utf8.DecodeRune(tempBytes[startIndex:])
				endIndex = startIndex + max(runeSize, 1)
				if endIndex > len(tempBytes) {
					endIndex = len(tempBytes)
				}
			}

			// 在合法的 endIndex 附近嘗試找斷句點
			potentialChunk := string(tempBytes[startIndex:endIndex])
			splitPoint := findSplitPoint(potentialChunk)
			if splitPoint > 0 && splitPoint < len(potentialChunk) {
				// 檢查 splitPoint 是否真的有意義 (不能太接近 startIndex)
				if splitPoint > len(potentialChunk)/2 || len(potentialChunk) < 100 { // 如果切點在後半段或片段很短，則使用
					endIndex = startIndex + splitPoint
				}
			}
		}

		chunks = append(chunks, string(tempBytes[startIndex:endIndex]))
		startIndex = endIndex
	}

	log.Printf("文本被分割成 %d 個片段。\n", len(chunks))
	return chunks
}

func main() {
	ctx := context.Background()
	// --- Load Configuration ---
	configFile, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Fatalf("無法讀取設定檔 config.yaml: %v", err)
	}

	var cfg Config
	err = yaml.Unmarshal(configFile, &cfg)
	if err != nil {
		log.Fatalf("無法解析設定檔 config.yaml: %v", err)
	}

	// --- 1. 讀取輸入文字檔 ---
	log.Printf("正在讀取輸入檔案: %s\n", cfg.InputFilename)
	inputTextBytes, err := os.ReadFile(cfg.InputFilename)
	if err != nil {
		log.Fatalf("無法讀取輸入檔案 %s: %v", cfg.InputFilename, err)
	}
	inputText := string(inputTextBytes)
	log.Printf("讀取到 %d 個位元組。\n", len(inputTextBytes))

	// --- 2. 初始化 Text-to-Speech 客戶端 ---
	log.Println("正在初始化 Text-to-Speech 客戶端...")
	// client, err := texttospeech.NewClient(ctx, option.WithCredentialsFile("path/to/your/keyfile.json"))
	client, err := texttospeech.NewClient(ctx)
	if err != nil {
		log.Fatalf("無法建立 Text-to-Speech 客戶端: %v", err)
	}
	defer client.Close()
	log.Println("Text-to-Speech 客戶端初始化完成。")

	// --- 3. 分割文本 ---
	textChunks := splitText(inputText, 200)

	// --- 4. 準備輸出檔案 ---
	log.Printf("準備寫入本地檔案: %s\n", cfg.OutputFilename)
	outputFile, err := os.Create(cfg.OutputFilename)
	if err != nil {
		log.Fatalf("無法建立輸出檔案 %s: %v", cfg.OutputFilename, err)
	}
	defer outputFile.Close()

	// --- 5. 逐一合成每個文本片段並寫入檔案 ---
	log.Println("開始逐片段合成語音...")
	totalAudioSize := 0
	for i, chunk := range textChunks {
		log.Printf("正在合成片段 %d / %d (%d 位元組)...\n", i+1, len(textChunks), len([]byte(chunk)))

		req := &texttospeechpb.SynthesizeSpeechRequest{
			Input: &texttospeechpb.SynthesisInput{
				InputSource: &texttospeechpb.SynthesisInput_Text{Text: chunk},
			},
			Voice: &texttospeechpb.VoiceSelectionParams{
				LanguageCode: cfg.LanguageCode,
				Name:         cfg.VoiceName,
			},
			AudioConfig: &texttospeechpb.AudioConfig{
				AudioEncoding: texttospeechpb.AudioEncoding_MP3,
				SpeakingRate:  cfg.SpeakingRate,
				Pitch:         cfg.Pitch,
			},
		}

		resp, err := client.SynthesizeSpeech(ctx, req)
		if err != nil {
			log.Printf("警告：合成片段 %d 時發生錯誤: %v\n", i+1, err)
			// 根據需求，你可以選擇跳過這個片段 (continue) 或終止程式 (log.Fatalf)
			continue // 這裡選擇跳過有問題的片段
		}

		// 將合成的音訊資料寫入檔案
		nBytes, err := outputFile.Write(resp.AudioContent)
		if err != nil {
			log.Fatalf("無法將音訊資料寫入檔案 %s: %v", cfg.OutputFilename, err)
		}
		totalAudioSize += nBytes
		log.Printf("片段 %d 合成完畢，寫入 %d 位元組。\n", i+1, nBytes)
	}

	log.Printf("所有片段合成完成！總共寫入 %d 位元組到 %s\n", totalAudioSize, cfg.OutputFilename)
	log.Println("注意：MP3 片段是直接串接的，可能在某些播放器或編輯器中有兼容性問題。")
}

// max 返回兩個整數中較大的那個 (輔助函數)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

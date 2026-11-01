//go:build windows

package observe

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"unicode/utf16"
)

const windowsOCRScript = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName System.Runtime.WindowsRuntime
[Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
[Windows.Globalization.Language, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
[Windows.Storage.StorageFile, Windows.Storage, ContentType=WindowsRuntime] | Out-Null
[Windows.Storage.Streams.IRandomAccessStream, Windows.Storage.Streams, ContentType=WindowsRuntime] | Out-Null
[Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics.Imaging, ContentType=WindowsRuntime] | Out-Null
[Windows.Graphics.Imaging.SoftwareBitmap, Windows.Graphics.Imaging, ContentType=WindowsRuntime] | Out-Null
[Windows.Media.Ocr.OcrResult, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
$method = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethodDefinition -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -like 'IAsyncOperation*' } | Select-Object -First 1
function Await($operation, $type) { $task = $method.MakeGenericMethod($type).Invoke($null, @($operation)); $task.Wait(); return $task.Result }
$language = [Windows.Globalization.Language]::new('zh-Hans-CN')
$engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($language)
if ($null -eq $engine) { throw 'Windows Chinese OCR language is unavailable' }
[Console]::WriteLine('{"ready":true}')
while ($null -ne ($line = [Console]::ReadLine())) {
 try {
  $path = ConvertFrom-Json $line
  $file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync($path)) ([Windows.Storage.StorageFile])
  $stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
  try {
   $decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
   $bitmap = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
   $result = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
   $response = @{ text = $result.Text } | ConvertTo-Json -Compress
  } finally { $stream.Dispose() }
 } catch { $response = @{ error = $_.Exception.Message } | ConvertTo-Json -Compress }
 [Console]::WriteLine($response)
}
`

type ocrWorker struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
}

func startOCR(stderr io.Writer) (*ocrWorker, error) {
	units := utf16.Encode([]rune(windowsOCRScript))
	raw := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], u)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	worker := &ocrWorker{cmd: cmd, input: input, output: bufio.NewScanner(output)}
	if !worker.output.Scan() {
		_ = worker.input.Close()
		_ = worker.cmd.Wait()
		return nil, fmt.Errorf("Windows Chinese OCR initialization failed; inspect log")
	}
	var ready struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(worker.output.Bytes(), &ready); err != nil || !ready.Ready {
		_ = worker.input.Close()
		_ = worker.cmd.Wait()
		return nil, fmt.Errorf("invalid Windows OCR initialization response")
	}
	return worker, nil
}
func (w *ocrWorker) recognize(path string) (string, error) {
	if err := json.NewEncoder(w.input).Encode(path); err != nil {
		return "", err
	}
	if !w.output.Scan() {
		if err := w.output.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("Windows OCR worker exited; inspect log")
	}
	var r struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.output.Bytes(), &r); err != nil {
		return "", err
	}
	if r.Error != "" {
		return "", fmt.Errorf("Windows OCR: %s", r.Error)
	}
	return r.Text, nil
}
func (w *ocrWorker) close() error {
	if err := w.input.Close(); err != nil {
		return err
	}
	return w.cmd.Wait()
}
func RecognizeText(path string, stderr io.Writer) (string, error) {
	w, err := startOCR(stderr)
	if err != nil {
		return "", err
	}
	defer w.close()
	return w.recognize(path)
}

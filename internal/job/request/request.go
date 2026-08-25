package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"Vylux/internal/jsonx"
	"Vylux/internal/queue"
)

// Normalized is the legacy-compatible request model consumed by the current job handler.
type Normalized struct {
	Type        string
	Hash        string
	Source      string
	Options     map[string]any
	CallbackURL string
}

var validJobTypes = map[string]bool{
	queue.TypeImageThumbnail: true,
	queue.TypeAudioTranscode: true,
	queue.TypeVideoCover:     true,
	queue.TypeVideoPreview:   true,
	queue.TypeVideoTranscode: true,
	queue.TypeVideoFull:      true,
}

type rawJobRequest struct {
	Type        string                     `json:"type"`
	Hash        string                     `json:"hash"`
	Source      sourceField                `json:"source"`
	Options     map[string]any             `json:"options,omitempty"`
	CallbackURL string                     `json:"callback_url"`
	AssetType   string                     `json:"asset_type"`
	Operation   string                     `json:"operation"`
	Pipeline    *structuredPipelineRequest `json:"pipeline,omitempty"`
	Delivery    *structuredDeliveryRequest `json:"delivery,omitempty"`
}

type sourceField struct {
	Hash       string
	Key        string
	structured bool
}

type structuredPipelineRequest struct {
	Analyze   bool                             `json:"analyze,omitempty"`
	Cover     *structuredVideoCoverRequest     `json:"cover,omitempty"`
	Outputs   []structuredImageOutput          `json:"outputs,omitempty"`
	Package   *structuredPackageRequest        `json:"package,omitempty"`
	Preview   *structuredVideoPreviewRequest   `json:"preview,omitempty"`
	Transcode *structuredVideoTranscodeRequest `json:"transcode,omitempty"`
	Downloads []structuredDownloadRequest      `json:"downloads,omitempty"`
	Waveform  *structuredWaveformRequest       `json:"waveform,omitempty"`
	Normalize *structuredEnabledRequest        `json:"normalize,omitempty"`
}

type structuredVideoCoverRequest struct {
	Enabled      bool    `json:"enabled"`
	TimestampSec float64 `json:"timestamp_sec,omitempty"`
}

type structuredVideoPreviewRequest struct {
	Enabled  bool    `json:"enabled"`
	StartSec float64 `json:"start_sec,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Width    int     `json:"width,omitempty"`
	FPS      int     `json:"fps,omitempty"`
	Format   string  `json:"format,omitempty"`
}

type structuredVideoTranscodeRequest struct {
	Enabled bool `json:"enabled"`
	Encrypt bool `json:"encrypt,omitempty"`
}

type structuredImageOutput struct {
	Variant string `json:"variant"`
	Width   int    `json:"width"`
	Height  int    `json:"height,omitempty"`
	Format  string `json:"format"`
}

type structuredPackageRequest struct {
	HLS *structuredHLSPackageRequest `json:"hls,omitempty"`
}

type structuredHLSPackageRequest struct {
	Enabled    bool                      `json:"enabled"`
	Profile    string                    `json:"profile,omitempty"`
	Encryption *structuredEnabledRequest `json:"encryption,omitempty"`
}

type structuredDownloadRequest struct {
	Profile string `json:"profile,omitempty"`
	Format  string `json:"format"`
	Bitrate string `json:"bitrate,omitempty"`
}

type structuredEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type structuredWaveformRequest struct {
	Enabled bool   `json:"enabled"`
	Profile string `json:"profile,omitempty"`
	Bins    int    `json:"bins,omitempty"`
}

type structuredDeliveryRequest struct {
	CallbackURL string `json:"callback_url"`
}

// Decode reads either the legacy or structured job request schema and normalizes it.
func Decode(r io.Reader) (Normalized, error) {
	var req rawJobRequest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Normalized{}, err
	}
	if req.isStructured() {
		return req.normalizeStructured()
	}
	if strings.EqualFold(strings.TrimSpace(req.Type), queue.TypeAudioTranscode) {
		return Normalized{}, fmt.Errorf("legacy audio requests are not supported; use POST /api/audio/jobs")
	}

	return req.normalizeLegacy(), nil
}

// DecodeAudioCreate reads the public audio create contract used by POST /api/audio/jobs.
func DecodeAudioCreate(r io.Reader) (Normalized, error) {
	var req rawJobRequest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Normalized{}, err
	}
	if req.Type != "" || req.Hash != "" || req.Options != nil || req.CallbackURL != "" || req.AssetType != "" || req.Operation != "" {
		return Normalized{}, fmt.Errorf("audio create requests must use the /api/audio/jobs contract without type/hash/options/callback_url/asset_type/operation fields")
	}

	return req.normalizeStructuredAudioProcess()
}

// DecodeVideoCreate reads the public video create contract used by POST /api/video/jobs.
func DecodeVideoCreate(r io.Reader) (Normalized, error) {
	var req rawJobRequest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Normalized{}, err
	}
	if req.Type != "" || req.Hash != "" || req.Options != nil || req.CallbackURL != "" || req.AssetType != "" || req.Operation != "" {
		return Normalized{}, fmt.Errorf("video create requests must use the /api/video/jobs contract without type/hash/options/callback_url/asset_type/operation fields")
	}

	return req.normalizeStructuredVideoProcess()
}

// Validate checks request shape and option schemas for the current normalized model.
func Validate(r Normalized) error {
	if !validJobTypes[r.Type] {
		return fmt.Errorf("unsupported job type: %q", r.Type)
	}
	if r.Hash == "" {
		return fmt.Errorf("hash is required")
	}
	if r.Source == "" {
		return fmt.Errorf("source is required")
	}
	if err := validateCallbackURL(r.CallbackURL); err != nil {
		return err
	}
	if err := validateJobOptions(r.Type, r.Options); err != nil {
		return err
	}

	return nil
}

// Canonicalize normalizes option maps into their canonical JSON shape.
func Canonicalize(r *Normalized) error {
	if r == nil {
		return nil
	}
	if r.Options == nil {
		r.Options = map[string]any{}
	}

	switch r.Type {
	case queue.TypeAudioTranscode:
		parsed, err := parseAudioTranscodeOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonicalizeAudioTranscodeOptions(&parsed)
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoCover:
		parsed, err := parseVideoCoverOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoPreview:
		parsed, err := parseVideoPreviewOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoTranscode:
		parsed, err := parseVideoTranscodeOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoFull:
		parsed, err := parseVideoFullOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonicalizeVideoFullOptions(&parsed)
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	}

	return nil
}

func (r *rawJobRequest) isStructured() bool {
	return r.AssetType != "" || r.Operation != "" || r.Pipeline != nil || r.Delivery != nil || r.Source.structured
}

func (r *rawJobRequest) normalizeLegacy() Normalized {
	return Normalized{
		Type:        r.Type,
		Hash:        r.Hash,
		Source:      r.Source.Key,
		Options:     r.Options,
		CallbackURL: r.CallbackURL,
	}
}

func (r *rawJobRequest) normalizeStructured() (Normalized, error) {
	if r.Type != "" || r.Hash != "" || r.Options != nil || r.CallbackURL != "" {
		return Normalized{}, fmt.Errorf("structured requests cannot include legacy type/hash/options/callback_url fields")
	}
	assetType := strings.ToLower(strings.TrimSpace(r.AssetType))
	operation := strings.ToLower(strings.TrimSpace(r.Operation))
	switch {
	case assetType == "audio" && operation == "process":
		return r.normalizeStructuredAudioProcess()
	case assetType == "image" && operation == "process":
		return r.normalizeStructuredImageProcess()
	case assetType == "video" && operation == "process":
		return r.normalizeStructuredVideoProcess()
	case assetType == "":
		return Normalized{}, fmt.Errorf("asset_type is required for structured requests")
	case operation == "":
		return Normalized{}, fmt.Errorf("operation is required for structured requests")
	default:
		return Normalized{}, fmt.Errorf("unsupported structured request: asset_type=%q operation=%q", assetType, operation)
	}
}

func (r *rawJobRequest) normalizeStructuredAudioProcess() (Normalized, error) {
	if strings.TrimSpace(r.Source.Hash) == "" {
		return Normalized{}, fmt.Errorf("source.hash is required for structured audio requests")
	}
	if strings.TrimSpace(r.Source.Key) == "" {
		return Normalized{}, fmt.Errorf("source.key is required for structured audio requests")
	}
	options, err := buildStructuredAudioOptions(r.Pipeline)
	if err != nil {
		return Normalized{}, err
	}
	callbackURL := ""
	if r.Delivery != nil {
		callbackURL = r.Delivery.CallbackURL
	}

	return Normalized{
		Type:        queue.TypeAudioTranscode,
		Hash:        r.Source.Hash,
		Source:      r.Source.Key,
		Options:     options,
		CallbackURL: callbackURL,
	}, nil
}

func (r *rawJobRequest) normalizeStructuredImageProcess() (Normalized, error) {
	if strings.TrimSpace(r.Source.Hash) == "" {
		return Normalized{}, fmt.Errorf("source.hash is required for structured image requests")
	}
	if strings.TrimSpace(r.Source.Key) == "" {
		return Normalized{}, fmt.Errorf("source.key is required for structured image requests")
	}
	options, err := buildStructuredImageOptions(r.Pipeline)
	if err != nil {
		return Normalized{}, err
	}
	callbackURL := ""
	if r.Delivery != nil {
		callbackURL = r.Delivery.CallbackURL
	}

	return Normalized{
		Type:        queue.TypeImageThumbnail,
		Hash:        r.Source.Hash,
		Source:      r.Source.Key,
		Options:     options,
		CallbackURL: callbackURL,
	}, nil
}

func (r *rawJobRequest) normalizeStructuredVideoProcess() (Normalized, error) {
	if strings.TrimSpace(r.Source.Hash) == "" {
		return Normalized{}, fmt.Errorf("source.hash is required for structured video requests")
	}
	if strings.TrimSpace(r.Source.Key) == "" {
		return Normalized{}, fmt.Errorf("source.key is required for structured video requests")
	}
	taskType, options, err := buildStructuredVideoRequest(r.Pipeline)
	if err != nil {
		return Normalized{}, err
	}
	callbackURL := ""
	if r.Delivery != nil {
		callbackURL = r.Delivery.CallbackURL
	}

	return Normalized{
		Type:        taskType,
		Hash:        r.Source.Hash,
		Source:      r.Source.Key,
		Options:     options,
		CallbackURL: callbackURL,
	}, nil
}

func buildStructuredAudioOptions(pipeline *structuredPipelineRequest) (map[string]any, error) {
	options := map[string]any{}
	if pipeline == nil {
		return options, nil
	}
	if pipeline.Waveform != nil && pipeline.Waveform.Enabled {
		profile := strings.ToLower(strings.TrimSpace(pipeline.Waveform.Profile))
		if profile != "" && profile != "waveform_standard" {
			return nil, fmt.Errorf("unsupported pipeline.waveform.profile: %q", pipeline.Waveform.Profile)
		}
		options["waveform"] = true
		if pipeline.Waveform.Bins > 0 {
			options["waveform_bins"] = pipeline.Waveform.Bins
		}
	}
	if pipeline.Normalize != nil && pipeline.Normalize.Enabled {
		return nil, fmt.Errorf("pipeline.normalize is not implemented yet")
	}
	if pipeline.Package != nil && pipeline.Package.HLS != nil {
		profile := strings.ToLower(strings.TrimSpace(pipeline.Package.HLS.Profile))
		if profile != "" && profile != "stream_aac_standard" {
			return nil, fmt.Errorf("unsupported pipeline.package.hls.profile: %q", pipeline.Package.HLS.Profile)
		}
		if pipeline.Package.HLS.Enabled {
			options["hls"] = true
		}
		if pipeline.Package.HLS.Encryption != nil && pipeline.Package.HLS.Encryption.Enabled {
			if !pipeline.Package.HLS.Enabled {
				return nil, fmt.Errorf("pipeline.package.hls.encryption requires pipeline.package.hls.enabled=true")
			}
			options["encrypt"] = true
		}
	}
	for _, download := range pipeline.Downloads {
		if err := applyStructuredAudioDownload(options, download); err != nil {
			return nil, err
		}
	}

	return options, nil
}

func buildStructuredImageOptions(pipeline *structuredPipelineRequest) (map[string]any, error) {
	options := map[string]any{}
	if pipeline == nil {
		return options, nil
	}
	if pipeline.Analyze {
		return nil, fmt.Errorf("unsupported structured image pipeline: analyze is not supported")
	}
	if pipeline.Cover != nil || pipeline.Preview != nil || pipeline.Transcode != nil {
		return nil, fmt.Errorf("unsupported structured image pipeline: video stages are not supported")
	}
	if pipeline.Package != nil {
		return nil, fmt.Errorf("unsupported structured image pipeline: package is not supported")
	}
	if len(pipeline.Downloads) > 0 {
		return nil, fmt.Errorf("unsupported structured image pipeline: downloads are not supported")
	}
	if pipeline.Waveform != nil && pipeline.Waveform.Enabled {
		return nil, fmt.Errorf("unsupported structured image pipeline: waveform is not supported")
	}
	if pipeline.Normalize != nil && pipeline.Normalize.Enabled {
		return nil, fmt.Errorf("unsupported structured image pipeline: normalize is not supported")
	}
	if len(pipeline.Outputs) == 0 {
		return options, nil
	}
	outputs := make([]map[string]any, 0, len(pipeline.Outputs))
	for _, output := range pipeline.Outputs {
		if strings.TrimSpace(output.Variant) == "" {
			return nil, fmt.Errorf("pipeline.outputs variant is required")
		}
		if output.Width <= 0 {
			return nil, fmt.Errorf("pipeline.outputs width must be greater than 0")
		}
		if strings.TrimSpace(output.Format) == "" {
			return nil, fmt.Errorf("pipeline.outputs format is required")
		}
		entry := map[string]any{
			"variant": output.Variant,
			"width":   output.Width,
			"format":  output.Format,
		}
		if output.Height > 0 {
			entry["height"] = output.Height
		}
		outputs = append(outputs, entry)
	}
	options["outputs"] = outputs
	return options, nil
}

func applyStructuredAudioDownload(options map[string]any, download structuredDownloadRequest) error {
	profile := strings.ToLower(strings.TrimSpace(download.Profile))
	switch profile {
	case "":
		// Fall through to explicit format mapping.
	case "download_mp3_high":
		if format := strings.ToLower(strings.TrimSpace(download.Format)); format != "" && format != "mp3" {
			return fmt.Errorf("download profile %q conflicts with format %q", download.Profile, download.Format)
		}
		options["mp3"] = true
		options["mp3_bitrate"] = "320k"
		return nil
	case "download_flac_standard":
		if format := strings.ToLower(strings.TrimSpace(download.Format)); format != "" && format != "flac" {
			return fmt.Errorf("download profile %q conflicts with format %q", download.Profile, download.Format)
		}
		if strings.TrimSpace(download.Bitrate) != "" {
			return fmt.Errorf("download profile %q does not accept bitrate", download.Profile)
		}
		options["flac"] = true
		return nil
	default:
		return fmt.Errorf("unsupported pipeline.downloads profile: %q", download.Profile)
	}

	switch strings.ToLower(strings.TrimSpace(download.Format)) {
	case "mp3":
		options["mp3"] = true
		if strings.TrimSpace(download.Bitrate) != "" {
			options["mp3_bitrate"] = download.Bitrate
		}
	case "flac":
		if strings.TrimSpace(download.Bitrate) != "" {
			return fmt.Errorf("pipeline.downloads format %q does not accept bitrate", download.Format)
		}
		options["flac"] = true
	case "":
		return fmt.Errorf("pipeline.downloads format is required")
	default:
		return fmt.Errorf("unsupported pipeline.downloads format: %q", download.Format)
	}

	return nil
}

func buildStructuredVideoRequest(pipeline *structuredPipelineRequest) (string, map[string]any, error) {
	if isDefaultStructuredVideoPipeline(pipeline) {
		return queue.TypeVideoFull, map[string]any{}, nil
	}
	if pipeline.Transcode != nil {
		return "", nil, fmt.Errorf("unsupported structured video pipeline: pipeline.transcode is not part of the public contract; use pipeline.package.hls")
	}
	if len(pipeline.Downloads) > 0 {
		return "", nil, fmt.Errorf("unsupported structured video pipeline: downloads are not supported yet")
	}
	if pipeline.Waveform != nil && pipeline.Waveform.Enabled {
		return "", nil, fmt.Errorf("unsupported structured video pipeline: waveform is not supported")
	}
	if pipeline.Normalize != nil && pipeline.Normalize.Enabled {
		return "", nil, fmt.Errorf("unsupported structured video pipeline: normalize is not supported")
	}

	hlsEnabled, encrypt, err := parseStructuredVideoHLS(pipeline.Package)
	if err != nil {
		return "", nil, err
	}
	coverEnabled := stageEnabled(pipeline.Cover)
	previewEnabled := stageEnabled(pipeline.Preview)
	deliverableCount := boolCount(coverEnabled, previewEnabled, hlsEnabled)
	if deliverableCount == 0 {
		return "", nil, nilUnsupportedVideoDeliverableSelection("no deliverables enabled")
	}

	switch {
	case coverEnabled && !previewEnabled && !hlsEnabled:
		options, err := structToOptionsMap(queue.VideoCoverOptions{TimestampSec: pipeline.Cover.TimestampSec})
		return queue.TypeVideoCover, options, err
	case !coverEnabled && previewEnabled && !hlsEnabled:
		options, err := structToOptionsMap(queue.VideoPreviewOptions{
			StartSec: pipeline.Preview.StartSec,
			Duration: pipeline.Preview.Duration,
			Width:    pipeline.Preview.Width,
			FPS:      pipeline.Preview.FPS,
			Format:   pipeline.Preview.Format,
		})
		return queue.TypeVideoPreview, options, err
	case !coverEnabled && !previewEnabled && hlsEnabled:
		options, err := structToOptionsMap(queue.VideoTranscodeOptions{Encrypt: encrypt})
		return queue.TypeVideoTranscode, options, err
	case coverEnabled && previewEnabled && hlsEnabled:
		options, err := structToOptionsMap(queue.VideoFullOptions{
			Cover:     coverOptionsForFull(pipeline.Cover),
			Preview:   previewOptionsForFull(pipeline.Preview),
			Transcode: &queue.VideoTranscodeOptions{Encrypt: encrypt},
		})
		return queue.TypeVideoFull, options, err
	default:
		return "", nil, nilUnsupportedVideoDeliverableSelection("supported combinations are cover only, preview only, package.hls only, or cover+preview+package.hls")
	}
}

func isDefaultStructuredVideoPipeline(pipeline *structuredPipelineRequest) bool {
	if pipeline == nil {
		return true
	}
	return !pipeline.Analyze &&
		!hasStructuredStage(pipeline.Cover) &&
		!hasStructuredStage(pipeline.Preview) &&
		!hasStructuredStage(pipeline.Transcode) &&
		len(pipeline.Outputs) == 0 &&
		(packageIsEmpty(pipeline.Package)) &&
		len(pipeline.Downloads) == 0 &&
		pipeline.Waveform == nil &&
		pipeline.Normalize == nil
}

func packageIsEmpty(pkg *structuredPackageRequest) bool {
	return pkg == nil || pkg.HLS == nil
}

func parseStructuredVideoHLS(pkg *structuredPackageRequest) (bool, bool, error) {
	if pkg == nil || pkg.HLS == nil {
		return false, false, nil
	}
	profile := strings.ToLower(strings.TrimSpace(pkg.HLS.Profile))
	if profile != "" && profile != "stream_video_standard" {
		return false, false, fmt.Errorf("unsupported structured video pipeline: unsupported pipeline.package.hls.profile: %q", pkg.HLS.Profile)
	}
	encrypt := pkg.HLS.Encryption != nil && pkg.HLS.Encryption.Enabled
	return pkg.HLS.Enabled, encrypt, nil
}

func hasStructuredStage[T any](stage *T) bool {
	return stage != nil
}

func stageEnabled(stage interface{ isEnabled() bool }) bool {
	if stage == nil {
		return false
	}
	return stage.isEnabled()
}

func (s *structuredVideoCoverRequest) isEnabled() bool     { return s != nil && s.Enabled }
func (s *structuredVideoPreviewRequest) isEnabled() bool   { return s != nil && s.Enabled }
func (s *structuredVideoTranscodeRequest) isEnabled() bool { return s != nil && s.Enabled }

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func coverOptionsForFull(stage *structuredVideoCoverRequest) *queue.VideoCoverOptions {
	if stage == nil {
		return nil
	}
	return &queue.VideoCoverOptions{TimestampSec: stage.TimestampSec}
}

func previewOptionsForFull(stage *structuredVideoPreviewRequest) *queue.VideoPreviewOptions {
	if stage == nil {
		return nil
	}
	return &queue.VideoPreviewOptions{
		StartSec: stage.StartSec,
		Duration: stage.Duration,
		Width:    stage.Width,
		FPS:      stage.FPS,
		Format:   stage.Format,
	}
}

func nilUnsupportedVideoDeliverableSelection(reason string) error {
	return fmt.Errorf("unsupported structured video pipeline: %s", reason)
}

func validateCallbackURL(raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("callback_url must be a valid URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("callback_url must use http:// or https://")
	}

	if u.Host == "" {
		return fmt.Errorf("callback_url must include a host")
	}

	return nil
}

func validateJobOptions(jobType string, opts map[string]any) error {
	switch jobType {
	case queue.TypeImageThumbnail:
		_, err := parseImageThumbnailOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeAudioTranscode:
		parsed, err := parseAudioTranscodeOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonicalizeAudioTranscodeOptions(&parsed)
		if parsed.Encrypt && (parsed.MP3 || parsed.FLAC) {
			return fmt.Errorf("invalid options: encrypted audio jobs cannot request mp3 or flac downloads")
		}
		if parsed.Encrypt && !parsed.HLS {
			return fmt.Errorf("invalid options: encrypted audio jobs require hls output")
		}
	case queue.TypeVideoCover:
		_, err := parseVideoCoverOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoPreview:
		_, err := parseVideoPreviewOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoTranscode:
		_, err := parseVideoTranscodeOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoFull:
		_, err := parseVideoFullOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	}

	return nil
}

type imageThumbnailOptions struct {
	Outputs []queue.ThumbnailOutput `json:"outputs,omitempty"`
}

func parseImageThumbnailOptions(opts map[string]any) (imageThumbnailOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[imageThumbnailOptions](opts)
}

func parseVideoCoverOptions(opts map[string]any) (queue.VideoCoverOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[queue.VideoCoverOptions](opts)
}

func parseAudioTranscodeOptions(opts map[string]any) (queue.AudioTranscodeOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[queue.AudioTranscodeOptions](opts)
}

func parseVideoPreviewOptions(opts map[string]any) (queue.VideoPreviewOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[queue.VideoPreviewOptions](opts)
}

func parseVideoTranscodeOptions(opts map[string]any) (queue.VideoTranscodeOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[queue.VideoTranscodeOptions](opts)
}

func parseVideoFullOptions(opts map[string]any) (queue.VideoFullOptions, error) {
	return jsonx.StrictCodec.DecodeStrict[queue.VideoFullOptions](opts)
}

func structToOptionsMap[T any](opts T) (map[string]any, error) {
	return jsonx.StrictCodec.ToMap(opts)
}

func canonicalizeVideoFullOptions(opts *queue.VideoFullOptions) {
	if opts == nil {
		return
	}
	if opts.Cover != nil && *opts.Cover == (queue.VideoCoverOptions{}) {
		opts.Cover = nil
	}
	if opts.Preview != nil && *opts.Preview == (queue.VideoPreviewOptions{}) {
		opts.Preview = nil
	}
	if opts.Transcode != nil && *opts.Transcode == (queue.VideoTranscodeOptions{}) {
		opts.Transcode = nil
	}
}

func canonicalizeAudioTranscodeOptions(opts *queue.AudioTranscodeOptions) {
	if opts == nil {
		return
	}
	if !opts.HLS && !opts.MP3 && !opts.FLAC && !opts.Waveform {
		opts.HLS = true
		opts.MP3 = true
		opts.FLAC = true
		opts.Waveform = true
	}
	if opts.Waveform && opts.WaveformBins <= 0 {
		opts.WaveformBins = 2048
	}
	if !opts.Waveform {
		opts.WaveformBins = 0
	}
	if opts.MP3 && strings.TrimSpace(opts.MP3Bitrate) == "" {
		opts.MP3Bitrate = "320k"
	}
	if !opts.MP3 {
		opts.MP3Bitrate = ""
	}
}

func (s *sourceField) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var key string
		if err := json.Unmarshal(trimmed, &key); err != nil {
			return err
		}
		s.Key = key
		return nil
	case '{':
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		var payload struct {
			Hash string `json:"hash"`
			Key  string `json:"key"`
		}
		if err := dec.Decode(&payload); err != nil {
			return err
		}
		s.Hash = payload.Hash
		s.Key = payload.Key
		s.structured = true
		return nil
	default:
		return fmt.Errorf("source must be a string or object")
	}
}

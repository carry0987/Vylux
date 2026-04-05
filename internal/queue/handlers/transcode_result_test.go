package handlers

import (
	"testing"

	"Vylux/internal/encryption"
	"Vylux/internal/video"
)

func TestBuildTranscodeResultIncludesSplitTracksAndEncryption(t *testing.T) {
	hash := "abcdef123456"
	result := &video.TranscodeResult{
		MasterPlaylistPath: "master.m3u8",
		AudioTracks: []video.PackagedAudioTrack{{
			ID:           "und_aac_2ch",
			Role:         "main",
			Language:     "und",
			Codec:        "aac",
			Channels:     2,
			Bitrate:      128000,
			PlaylistPath: "audio/und_aac_2ch/playlist.m3u8",
			InitPath:     "audio/und_aac_2ch/init.mp4",
			Segments:     []string{"seg-1", "seg-2"},
		}},
		VideoTracks: []video.PackagedVideoTrack{{
			ID:           "r720_h264",
			Codec:        video.CodecH264,
			Width:        1280,
			Height:       720,
			Bitrate:      2500000,
			PlaylistPath: "video/r720_h264/playlist.m3u8",
			InitPath:     "video/r720_h264/init.mp4",
			Segments:     []string{"seg-1", "seg-2", "seg-3"},
			AudioTrackID: "und_aac_2ch",
		}},
	}
	material := &encryption.Material{
		KeyID:            []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		ProtectionScheme: encryption.DefaultProtectionScheme,
		KeyURI:           "https://media.example.com/api/key/abcdef123456",
	}

	out := buildTranscodeResult(hash, result, []string{"videos/ab/abcdef123456/master.m3u8"}, material)

	if out.Streaming.Protocol != "hls" {
		t.Fatalf("protocol = %q, want hls", out.Streaming.Protocol)
	}
	if out.Streaming.Container != "cmaf" {
		t.Fatalf("container = %q, want cmaf", out.Streaming.Container)
	}
	if !out.Streaming.Encrypted {
		t.Fatal("expected encrypted streaming result")
	}
	if out.Streaming.MasterPlaylist != "videos/ab/abcdef123456/master.m3u8" {
		t.Fatalf("master playlist = %q", out.Streaming.MasterPlaylist)
	}
	if out.Streaming.DefaultAudioTrackID != "und_aac_2ch" {
		t.Fatalf("default audio track = %q", out.Streaming.DefaultAudioTrackID)
	}
	if len(out.AudioTracks) != 1 {
		t.Fatalf("audio track count = %d, want 1", len(out.AudioTracks))
	}
	if out.AudioTracks[0].Playlist != "videos/ab/abcdef123456/audio/und_aac_2ch/playlist.m3u8" {
		t.Fatalf("audio playlist = %q", out.AudioTracks[0].Playlist)
	}
	if len(out.VideoTracks) != 1 {
		t.Fatalf("video track count = %d, want 1", len(out.VideoTracks))
	}
	if got := out.VideoTracks[0].AudioTrackIDs; len(got) != 1 || got[0] != "und_aac_2ch" {
		t.Fatalf("audio track ids = %v, want [und_aac_2ch]", got)
	}
	if out.Encryption == nil {
		t.Fatal("expected encryption metadata")
	}
	if out.Encryption.Scheme != encryption.DefaultProtectionScheme {
		t.Fatalf("scheme = %q, want %q", out.Encryption.Scheme, encryption.DefaultProtectionScheme)
	}
	if out.Encryption.KID != "00112233445566778899aabbccddeeff" {
		t.Fatalf("kid = %q", out.Encryption.KID)
	}
	if out.Encryption.KeyEndpoint != material.KeyURI {
		t.Fatalf("key endpoint = %q, want %q", out.Encryption.KeyEndpoint, material.KeyURI)
	}
	if len(out.UploadedKeys) != 1 || out.UploadedKeys[0] != "videos/ab/abcdef123456/master.m3u8" {
		t.Fatalf("uploaded keys = %v", out.UploadedKeys)
	}
}

func TestBuildTranscodeResultWithoutAudioOrEncryption(t *testing.T) {
	hash := "123456"
	result := &video.TranscodeResult{
		MasterPlaylistPath: "master.m3u8",
		VideoTracks: []video.PackagedVideoTrack{{
			ID:           "r480_av1",
			Codec:        video.CodecAV1,
			Width:        854,
			Height:       480,
			Bitrate:      600000,
			PlaylistPath: "video/r480_av1/playlist.m3u8",
			InitPath:     "video/r480_av1/init.mp4",
			Segments:     []string{"seg-1"},
		}},
	}

	out := buildTranscodeResult(hash, result, nil, nil)

	if out.Streaming.Encrypted {
		t.Fatal("expected unencrypted streaming result")
	}
	if out.Streaming.DefaultAudioTrackID != "" {
		t.Fatalf("default audio track = %q, want empty", out.Streaming.DefaultAudioTrackID)
	}
	if out.Encryption != nil {
		t.Fatalf("expected no encryption metadata, got %+v", out.Encryption)
	}
	if got := out.VideoTracks[0].AudioTrackIDs; len(got) != 0 {
		t.Fatalf("audio track ids = %v, want empty", got)
	}
	if out.Streaming.MasterPlaylist != "videos/12/123456/master.m3u8" {
		t.Fatalf("master playlist = %q", out.Streaming.MasterPlaylist)
	}
}

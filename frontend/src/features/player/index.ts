// player 的公开入口：生命周期、订阅、曲目转换、队列类型与可播判定。外部组件不导入 feature 内部文件。
export { PlayerProvider, usePlayer, usePlayerEngine, usePlayerProgress, usePlayerProgressSelector, usePlayerSelector } from "./player-provider";
export { usePlaybackCapabilities } from "./hooks/use-playback-capabilities";
export { useCurrentTrackMarker } from "./hooks/use-current-track-marker";
export { isPlaybackActive, PlayerController, type AddResult, type PlayerControllerDeps, type PlayerProgress, type PlayerSnapshot, type ZoneApi } from "./zones/controller";
export type { Device, PlaybackFailureReason, PlaybackSettings, PlayerEvent, PlayerFault, PlayerStatus, Zone } from "./zones/model";
export { playModes, upNext, MAX_QUEUE_ENTRIES, type PlayMode, type PlayerTrack, type QueueEntry, type QueueState, type SourceFacts, type TrackDisplayMetadata } from "./engine/queue";
export { playerTrackOf, playerTracksOf, playerTrackOfSummary, type SafeTrack } from "./engine/tracks";
export { unplayableText, type CapabilityTable, type TrackVerdict } from "./engine/capability";
export { AlbumPlaybackActions } from "./components/album-playback-actions";
export { PlayerBar } from "./components/player-bar";
export { FullscreenPlayer } from "./components/fullscreen-player";
export { PlayerAnnouncement } from "./components/player-feedback";
export { PlaybackSettingsForm } from "./components/playback-settings-form";

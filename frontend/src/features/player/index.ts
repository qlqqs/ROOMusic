// player 的公开入口：生命周期、订阅、曲目转换、队列语义与浏览器能力。外部组件不导入 feature 内部文件。
export { PlayerProvider, usePlayer, usePlayerEngine, usePlayerProgress, usePlayerSelector } from "./player-provider";
export { usePlaybackCapabilities } from "./hooks/use-playback-capabilities";
export { PlayerEngine, type AddResult, type EngineDeps, type PlayerEvent, type PlayerFault, type PlayerProgress, type PlayerSnapshot, type PlayerStatus, type PlaybackFailureReason } from "./engine/player-engine";
export { playModes, upNext, MAX_QUEUE_ENTRIES, type PlayMode, type PlayerTrack, type QueueEntry, type QueueState, type SourceFacts } from "./engine/queue";
export { playerTrackOf, playerTracksOf } from "./engine/tracks";
export { unplayableText, type CapabilityTable, type TrackVerdict } from "./engine/capability";

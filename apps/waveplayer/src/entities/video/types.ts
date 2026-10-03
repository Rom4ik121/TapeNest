export type VideoSource = 'youtube' | 'vk' | 'rutube';
export type VideoStatus = 'queued' | 'running' | 'done' | 'failed';

export interface VideoFile {
  fileName: string;
  sizeBytes: number;
  mimeType: string;
  durationSec: number;
  width: number;
  height: number;
  expiresAt: string;
}

export interface VideoProgress {
  stage: string;
  pct: number;
}

export interface VideoJob {
  id: string;
  url: string;
  source: VideoSource;
  status: VideoStatus;
  stage?: string;
  progress?: VideoProgress;
  title?: string;
  posterUrl?: string;
  errorKind?: string;
  errorMessage?: string;
  file?: VideoFile;
  createdAt: string;
  updatedAt: string;
  finishedAt?: string;
}

export interface VideoPage {
  items: VideoJob[];
  nextCursor: string | null;
}

export interface FileLink {
  url: string;
}

export type EditSpeed = 0.5 | 1 | 1.5 | 2;
export type EditRotate = 0 | 90 | 180 | 270;
export type EditTransition = 'none' | 'fade' | 'wipe';

export interface EditCrop {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface ComposeClipBody {
  jobId: string;
  inSec: number;
  outSec: number;
  speed: number;
  volume: number;
  crop: EditCrop;
  rotate: EditRotate;
  transition: EditTransition;
  transitionSec: number;
}

export interface ComposeTextBody {
  text: string;
  startSec: number;
  endSec: number;
  x: number;
  y: number;
}

export interface ComposeMusicBody {
  jobId: string;
  inSec: number;
  volume: number;
  offsetSec: number;
}

/** Timeline export. Every jobId must already belong to the caller. */
export interface ComposeRequest {
  title: string;
  clips: ComposeClipBody[];
  texts: ComposeTextBody[];
  music: ComposeMusicBody | null;
}

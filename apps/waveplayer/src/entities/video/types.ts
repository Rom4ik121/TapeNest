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

import { useEffect, useRef, type RefObject } from 'react';
import { useTranslation } from 'react-i18next';
import { placeAt, type Project, type TimelineClip } from './model';

interface PreviewProps {
  project: Project;
  time: number;
  playing: boolean;
  urls: Readonly<Record<string, string | undefined>>;
}

export function PreviewStage({ project, time, playing, urls }: PreviewProps) {
  const { t } = useTranslation();
  const place = placeAt(project, time);
  const primary = useRef<HTMLVideoElement>(null);
  const secondary = useRef<HTMLVideoElement>(null);
  const music = useRef<HTMLAudioElement>(null);
  const clip = place ? project.clips[place.clip.index] : undefined;
  const incoming = place?.incoming ? project.clips[place.incoming.index] : undefined;

  useEffect(() => {
    const node = music.current;
    const bed = project.music;
    if (!node || !bed) return;
    const url = urls[bed.jobId];
    if (!url) return;
    if (node.getAttribute('src') !== url) node.src = url;
    const at = bed.inSec + Math.max(0, time - bed.offsetSec);
    if (Math.abs(node.currentTime - at) > 0.35) node.currentTime = at;
    node.volume = Math.min(1, Math.max(0, bed.volume));
    playOrPause(node, playing);
  }, [playing, project.music, time, urls]);

  return (
    <div
      aria-label={t('videos.editor.preview')}
      className="relative aspect-video overflow-hidden rounded-3xl bg-ink shadow-card"
    >
      {clip && place && (
        <ClipVideo
          videoRef={primary}
          clip={clip}
          url={urls[clip.jobId]}
          sourceSec={place.clip.sourceSec}
          playing={playing && !incoming}
          opacity={incoming ? 1 - place.mix : 1}
        />
      )}
      {incoming && place?.incoming && (
        <ClipVideo
          videoRef={secondary}
          clip={incoming}
          url={urls[incoming.jobId]}
          sourceSec={place.incoming.sourceSec}
          playing={playing}
          opacity={place.mix}
        />
      )}
      {project.texts
        .filter((cue) => time >= cue.startSec && time < cue.endSec)
        .map((cue) => (
          <span
            key={cue.id}
            className="pointer-events-none absolute max-w-[88%] -translate-x-1/2 -translate-y-1/2 text-center text-lg font-extrabold text-cream drop-shadow-[0_2px_6px_#16141C]"
            style={{ left: `${cue.x * 100}%`, top: `${cue.y * 100}%` }}
          >
            {cue.text}
          </span>
        ))}
      {project.music && <audio ref={music} />}
    </div>
  );
}

function playOrPause(node: HTMLMediaElement, playing: boolean) {
  try {
    if (playing) void node.play().catch(() => undefined);
    else node.pause();
  } catch {
    // jsdom has no media playback.
  }
}

function ClipVideo({
  videoRef,
  clip,
  url,
  sourceSec,
  playing,
  opacity,
}: {
  videoRef: RefObject<HTMLVideoElement>;
  clip: TimelineClip;
  url: string | undefined;
  sourceSec: number;
  playing: boolean;
  opacity: number;
}) {
  useEffect(() => {
    const node = videoRef.current;
    if (!node || !url) return;
    if (node.getAttribute('src') !== url) node.src = url;
    node.playbackRate = clip.speed;
    node.volume = Math.min(1, Math.max(0, clip.volume));
    if (Math.abs(node.currentTime - sourceSec) > 0.35) node.currentTime = sourceSec;
    playOrPause(node, playing);
  }, [clip.speed, clip.volume, playing, sourceSec, url, videoRef]);

  const { crop, rotate } = clip;
  return (
    <video
      ref={videoRef}
      playsInline
      poster={clip.posterUrl}
      className="absolute max-w-none bg-ink"
      style={{
        width: `${100 / crop.w}%`,
        height: `${100 / crop.h}%`,
        left: `${(-100 * crop.x) / crop.w}%`,
        top: `${(-100 * crop.y) / crop.h}%`,
        transform: `rotate(${rotate}deg)`,
        opacity,
      }}
    />
  );
}

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TITLES } from '@/mocks/data';
import type { Title } from '@/shared/api/types';
import { FilePicker } from './FilePicker';

const asTitle = (i: number): Title => ({ ...TITLES[i]!, inWatchlist: false });
const movie = asTitle(TITLES.findIndex((t) => t.kind === 'movie'));
const series = asTitle(TITLES.findIndex((t) => t.kind === 'series'));

describe('FilePicker', () => {
  it('movie: lists versions best-first and selects on click', () => {
    const onSelect = vi.fn();
    render(<FilePicker title={movie} selectedId={null} onSelect={onSelect} />);
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(movie.files.length);
    expect(radios[0]).toHaveTextContent('2160p');
    fireEvent.click(radios[1]!);
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ quality: '1080p' }));
  });

  it('series: season tabs switch the episode list, selected file is marked', () => {
    const selected = series.files.find((f) => f.season === 1 && f.episode === 2 && f.quality === '720p')!;
    render(<FilePicker title={series} selectedId={selected.id} onSelect={() => undefined} />);
    const tabs = screen.getAllByRole('tab');
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true');
    expect(screen.getAllByRole('radio', { checked: true })).toHaveLength(1);
    expect(screen.getByText(/Выбрано/)).toHaveTextContent(selected.name);
    fireEvent.click(tabs[1]!);
    expect(tabs[1]).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryAllByRole('radio', { checked: true })).toHaveLength(0);
  });
});

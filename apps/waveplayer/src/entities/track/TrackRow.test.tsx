import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { track } from '../../../test/fixtures';
import { TrackRow } from './TrackRow';

describe('<TrackRow>', () => {
  it('renders localized controls and fires callbacks', () => {
    const onPlay = vi.fn();
    const onLike = vi.fn();
    const onMore = vi.fn();
    const tr = { ...track('a', 125), liked: true };
    render(
      <ul>
        <TrackRow track={tr} active={false} playing={false} onPlay={onPlay} onLike={onLike} onMore={onMore} />
      </ul>,
    );
    expect(screen.getByText('Title a')).toBeInTheDocument();
    expect(screen.getByText(/2:05/)).toBeInTheDocument();
    fireEvent.click(screen.getByText('Title a'));
    expect(onPlay).toHaveBeenCalledWith(tr);
    fireEvent.click(screen.getByRole('button', { name: 'Убрать из «Мне нравится»' }));
    expect(onLike).toHaveBeenCalledWith(tr);
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }));
    expect(onMore).toHaveBeenCalledWith(tr);
  });
});

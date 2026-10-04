import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { EditorTabs } from './EditorTabs';

describe('EditorTabs', () => {
  it('exposes a horizontal tab bar and switches', () => {
    const onChange = vi.fn();
    render(
      <EditorTabs
        tabs={[
          { id: 'clip', label: 'Клип' },
          { id: 'export', label: 'Экспорт' },
        ]}
        value="clip"
        onChange={onChange}
      />,
    );
    const clip = screen.getByRole('tab', { name: 'Клип' });
    expect(clip).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(screen.getByRole('tab', { name: 'Экспорт' }));
    expect(onChange).toHaveBeenCalledWith('export');
  });
});

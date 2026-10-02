import { describe, expect, it } from 'vitest';
import { mediaUrl } from './media';

describe('mediaUrl', () => {
  it('keeps same-origin relative links when no API origin is configured', () => {
    expect(mediaUrl('/api/v1/stream/covers/al-1?size=300&sig=x', '')).toBe('/api/v1/stream/covers/al-1?size=300&sig=x');
  });

  it('resolves gateway-relative links against VITE_API_URL', () => {
    expect(mediaUrl('/api/v1/stream/tracks/t?exp=1&sig=s', 'https://api.example.com')).toBe(
      'https://api.example.com/api/v1/stream/tracks/t?exp=1&sig=s',
    );
  });

  it('leaves absolute, protocol-relative, blob and null URLs untouched', () => {
    expect(mediaUrl('https://cdn.example.com/a.jpg', 'https://api.example.com')).toBe('https://cdn.example.com/a.jpg');
    expect(mediaUrl('//cdn.example.com/a.jpg', 'https://api.example.com')).toBe('//cdn.example.com/a.jpg');
    expect(mediaUrl('blob:abc', 'https://api.example.com')).toBe('blob:abc');
    expect(mediaUrl(null, 'https://api.example.com')).toBeNull();
  });
});

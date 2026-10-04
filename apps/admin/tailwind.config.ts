import type { Config } from 'tailwindcss';

const token = (name: string) => `rgb(var(--c-${name}) / <alpha-value>)`;

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        cream: '#E7E4DE',
        amber: '#EEAA11',
        teal: '#4FB3B3',
        magenta: '#BB3381',
        plum: '#3F1D50',
        ink: '#16141C',
        bg: token('bg'),
        surface: token('surface'),
        'surface-2': token('surface-2'),
        fg: token('fg'),
        muted: token('muted'),
        line: token('line'),
        accent: token('accent'),
        'accent-fg': token('accent-fg'),
        highlight: token('highlight'),
        danger: token('danger'),
        'danger-fg': token('danger-fg'),
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'Helvetica Neue', 'Arial', 'sans-serif'],
      },
      boxShadow: {
        card: '0 1px 2px rgb(22 20 28 / 0.06), 0 8px 24px -12px rgb(22 20 28 / 0.25)',
      },
    },
  },
  plugins: [],
} satisfies Config;

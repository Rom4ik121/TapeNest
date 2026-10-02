import type { Config } from 'tailwindcss';

/** Semantic tokens → CSS variables (see src/shared/theme/theme.css). */
const token = (name: string) => `rgb(var(--c-${name}) / <alpha-value>)`;

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        // raw brand palette
        cream: '#E7E4DE',
        amber: '#EEAA11',
        teal: '#4FB3B3',
        magenta: '#BB3381',
        plum: '#3F1D50',
        ink: '#16141C',
        // semantic (theme-aware)
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
      backgroundImage: {
        'grad-01': 'linear-gradient(135deg, #E7E4DE 0%, #EEAA11 100%)',
        'grad-02': 'linear-gradient(135deg, #EEAA11 0%, #BB3381 100%)',
        'grad-03': 'linear-gradient(135deg, #4FB3B3 0%, #BB3381 100%)',
        'grad-04': 'linear-gradient(160deg, #EEAA11 0%, #BB3381 34%, #3F1D50 70%, #16141C 100%)',
        'grad-05': 'linear-gradient(135deg, #BB3381 0%, #3F1D50 100%)',
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'Helvetica Neue', 'Arial', 'sans-serif'],
      },
      borderRadius: { '4xl': '2rem' },
      boxShadow: {
        card: '0 1px 2px rgb(22 20 28 / 0.06), 0 8px 24px -12px rgb(22 20 28 / 0.25)',
        glow: '0 10px 40px -10px rgb(187 51 129 / 0.55)',
      },
      keyframes: {
        'slide-up': { from: { transform: 'translateY(100%)' }, to: { transform: 'translateY(0)' } },
        'fade-in': { from: { opacity: '0' }, to: { opacity: '1' } },
        eq: { '0%,100%': { transform: 'scaleY(0.3)' }, '50%': { transform: 'scaleY(1)' } },
        float: {
          '0%,100%': { transform: 'translate3d(0,0,0) scale(1)' },
          '50%': { transform: 'translate3d(0,-12px,0) scale(1.04)' },
        },
        'spin-slow': { to: { transform: 'rotate(360deg)' } },
        ripple: {
          '0%': { transform: 'scale(0.92)', opacity: '0.7' },
          '100%': { transform: 'scale(1.12)', opacity: '0' },
        },
      },
      animation: {
        'slide-up': 'slide-up 0.32s cubic-bezier(0.32,0.72,0,1)',
        'fade-in': 'fade-in 0.2s ease-out',
        eq: 'eq 0.9s ease-in-out infinite',
        float: 'float 6s ease-in-out infinite',
        'spin-slow': 'spin-slow 18s linear infinite',
        ripple: 'ripple 2.8s cubic-bezier(0.2,0.6,0.3,1) infinite',
      },
    },
  },
  plugins: [],
} satisfies Config;

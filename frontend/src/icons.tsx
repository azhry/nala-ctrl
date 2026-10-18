import type { ReactNode, SVGProps } from 'react';

export type IconName = 'folder' | 'folder-plus' | 'server' | 'play' | 'stop' | 'refresh' | 'chevron' | 'terminal' | 'warning' | 'close' | 'check' | 'arrow' | 'spark';

export function Icon({ name, ...props }: SVGProps<SVGSVGElement> & { name: IconName }) {
  const common = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const };
  const paths: Record<IconName, ReactNode> = {
    folder: <><path {...common} d="M3.5 6.8A1.8 1.8 0 0 1 5.3 5h4l1.8 1.8h7.6a1.8 1.8 0 0 1 1.8 1.8v8.1a1.8 1.8 0 0 1-1.8 1.8H5.3a1.8 1.8 0 0 1-1.8-1.8z"/><path {...common} d="M3.7 9.2h16.1"/></>,
    'folder-plus': <><path {...common} d="M2.8 7A1.8 1.8 0 0 1 4.6 5.2h4.1l1.8 1.8h6.9a1.8 1.8 0 0 1 1.8 1.8v6.4a1.8 1.8 0 0 1-1.8 1.8H4.6a1.8 1.8 0 0 1-1.8-1.8z"/><path {...common} d="M12 10.3v5.2m-2.6-2.6h5.2"/></>,
    server: <><rect {...common} x="3.5" y="4" width="17" height="6.2" rx="1.5"/><rect {...common} x="3.5" y="13.8" width="17" height="6.2" rx="1.5"/><path {...common} d="M7 7.1h.01M10 7.1h7m-10 9.8h.01m3-.01h7"/></>,
    play: <><path {...common} d="M8.2 5.7a1 1 0 0 1 1.5-.9l8.2 5.2a1.2 1.2 0 0 1 0 2l-8.2 5.2a1 1 0 0 1-1.5-.9z"/></>,
    stop: <><rect {...common} x="5.5" y="5.5" width="13" height="13" rx="2"/></>,
    refresh: <><path {...common} d="M20 7v5h-5M4 17v-5h5"/><path {...common} d="M5.8 9A7 7 0 0 1 18 6.8L20 12M4 12l2 5.2A7 7 0 0 0 18.2 15"/></>,
    chevron: <><path {...common} d="m9 5 7 7-7 7"/></>,
    terminal: <><rect {...common} x="3.2" y="4.2" width="17.6" height="15.6" rx="2"/><path {...common} d="m7 9 2.7 2.5L7 14m5.5 0h4.2"/></>,
    warning: <><path {...common} d="M10.3 4.9 3.8 16.2a1.8 1.8 0 0 0 1.6 2.7h13.2a1.8 1.8 0 0 0 1.6-2.7L13.7 4.9a2 2 0 0 0-3.4 0Z"/><path {...common} d="M12 9v4m0 2.6h.01"/></>,
    close: <><path {...common} d="m6 6 12 12M18 6 6 18"/></>,
    check: <><path {...common} d="m5 12.5 4.3 4.2L19 7"/></>,
    arrow: <><path {...common} d="M5 12h14m-6-6 6 6-6 6"/></>,
    spark: <><path {...common} d="m12 3 1.6 6.1L20 11l-6.4 1.9L12 19l-1.6-6.1L4 11l6.4-1.9L12 3Z"/><path {...common} d="m19 15 .7 2.3L22 18l-2.3.7L19 21l-.7-2.3L16 18l2.3-.7L19 15Z"/></>,
  };

  return <svg aria-hidden="true" viewBox="0 0 24 24" {...common} {...props}>{paths[name]}</svg>;
}

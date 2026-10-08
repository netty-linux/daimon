const paths = {
  menu: 'M4 6h16M4 12h16M4 18h16', conversations: 'M4 5h16v12H8l-4 3V5Z M8 9h8M8 13h5',
  memory: 'M12 3 3 12l9 9 9-9-9-9Z M12 8v8M8 12h8', computer: 'M3 4h18v13H3z M8 21h8M12 17v4',
  settings: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z M12 2v3M12 19v3M2 12h3M19 12h3M5 5l2 2M17 17l2 2M5 19l2-2M17 7l2-2',
  more: 'M5 12h.01M12 12h.01M19 12h.01', plus: 'M12 5v14M5 12h14', send: 'M12 19V5M6 11l6-6 6 6',
} as const;
export function Icon({ name }: { name: keyof typeof paths }) {
  return <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d={paths[name]}/></svg>;
}

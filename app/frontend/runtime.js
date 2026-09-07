// Keep the actual UI shared; only the source of room data changes for a demo.
const runtime = new URLSearchParams(location.search).get('demo') === '1'
  ? await import('./demo-runtime.js')
  : await import('/wails/runtime.js');
export const Call = runtime.Call;
export const Events = runtime.Events;

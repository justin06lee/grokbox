// The portable presentation compiles the real app against an in-memory transport.
// Markup, styling, message rendering and interactions are the production files.
const base = new URL('./frontend/', import.meta.url);
const result = await Bun.build({
  entrypoints: [new URL('app.js', base).pathname], target: 'browser', minify: true,
  // Stamped like the Go build is, so the footer never drifts from the release.
  define: { DEMO_VERSION: JSON.stringify(process.env.VERSION || 'dev') },
  plugins: [{ name: 'demo-transport', setup(build) {
    build.onResolve({ filter: /^\.\/runtime\.js$/ }, () => ({ path: new URL('demo-runtime.js', base).pathname }));
  } }],
});
if (!result.success) throw new Error(result.logs.join('\n'));
let html = await Bun.file(new URL('index.html', base)).text();
html = html.replace('<link rel="stylesheet" href="/style.css" />', `<style>${await Bun.file(new URL('style.css', base)).text()}</style>`);
html = html.replace('src="/crate.svg"', `src="data:image/svg+xml;base64,${Buffer.from(await Bun.file(new URL('crate.svg', base)).text()).toString('base64')}"`);
html = html.replace('<script type="module" src="/app.js"></script>', `<script type="module">${(await result.outputs[0].text()).replaceAll('</script', '<\\/script')}</script>`);
const output = new URL('../dist/Grok Box Demo.html', import.meta.url);
await Bun.write(output, html);
console.log(decodeURIComponent(output.pathname));

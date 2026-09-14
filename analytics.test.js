const assert = require('node:assert/strict');
const { test } = require('node:test');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/analytics.js`, 'utf8');
function run(host, path='/workspace.html', hash='#records', search='?project=private-project&token=private-token') {
 const handlers={}, scripts=[];
 const location={hostname:host,pathname:path,hash,search};
 const window={history:{pushState(){},replaceState(){}},addEventListener(name,fn){handlers[name]=fn;}};window.top=window.self=window;
 const document={createElement(){return {};},head:{append(s){scripts.push(s);}}};
 const context=vm.createContext({window,document,location});vm.runInContext(source,context);
 return {window,location,handlers,scripts,context};
}
test('hosted workspace reports safe views without record or project data',()=>{
 const r=run('context.integ.life');r.location.hash='#files';r.handlers.hashchange();r.handlers.popstate();
 vm.runInContext(source,r.context);
 const commands=r.window.dataLayer.map(c=>Array.from(c));
 assert.equal(r.scripts.length,1);assert.equal(commands.filter(c=>c[1]==='page_view').length,2);
 const body=JSON.stringify(commands);assert.ok(body.includes('/workspace/files'));assert.ok(!body.includes('private-'));
});
test('self-hosted deployments do not load the production Google tag',()=>{assert.equal(run('localhost').scripts.length,0);assert.equal(run('my-context.example').scripts.length,0);});

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { workspaceURL, homeURL, workspaceIntent, readSession } = require('./navigation');
function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return { getItem: key => values.get(key) || null, removeItem: key => values.delete(key) };
}
const response = (status, body) => ({ status, ok: status >= 200 && status < 300, json: async () => body });
const options = (fetcher, store = storage()) => ({ api:'https://context-api.integ.life', fetcher, storage:store, tokenKey:'token', userKey:'user' });

test('the workspace and product page round-trip project, view, locale and local API', () => {
  const source = 'http://localhost:4173/workspace.html?project=project-a&api=http%3A%2F%2F127.0.0.1%3A8401&locale=en#plugins';
  const about = new URL(homeURL(source, 'zh-CN', true), source);
  assert.equal(about.searchParams.get('page'), 'about');
  assert.equal(workspaceIntent(about.href), false);
  const destination = new URL(workspaceURL(about.href, 'ms'), source);
  assert.equal(destination.pathname, '/workspace.html');
  assert.equal(destination.searchParams.get('project'), 'project-a');
  assert.equal(destination.searchParams.get('api'), 'http://127.0.0.1:8401');
  assert.equal(destination.searchParams.get('locale'), 'ms');
  assert.equal(destination.hash, '#plugins');
  assert.equal(destination.searchParams.has('page'), false);
});
test('public sections stay public, old workspace deep links and auth callbacks keep their destination', () => {
  for (const suffix of ['', '#how', '#connect-skill', '?page=about&project=p&view=state']) {
    assert.equal(workspaceIntent('https://context.integ.life/' + suffix), false, suffix);
  }
  for (const suffix of ['?project=p#state', '#records', '?auth=complete&project=p#plugins']) {
    assert.equal(workspaceIntent('https://context.integ.life/' + suffix), true, suffix);
  }
  const callback = workspaceURL('https://context.integ.life/?auth=complete&project=p#state', 'hi');
  assert.match(callback, /auth=complete/);
  assert.match(callback, /project=p/);
  assert.ok(callback.endsWith('#state'));
});
test('logout returns to the guest root without protected project or callback state', () => {
  const target = new URL(homeURL('https://context.integ.life/workspace.html?project=p&auth=complete#plugins', 'zh-CN'), 'https://context.integ.life');
  assert.equal(target.pathname, '/');
  assert.equal(target.search, '?locale=zh-CN');
  assert.equal(target.hash, '');
});
test('a central cookie wins over a cached legacy identity', async () => {
  const store = storage({token:'legacy',user:'old-user'});
  const calls = [];
  const user = await readSession(options(async (url, init) => { calls.push({url,init}); return response(200,{id:'central'}); },store));
  assert.equal(user.id,'central');
  assert.equal(calls.length,1);
  assert.deepEqual(calls[0].init.headers,{});
  assert.equal(calls[0].init.credentials,'include');
  assert.equal(store.getItem('token'),null);
  assert.equal(store.getItem('user'),null);
});
test('legacy bearer login works only after the cookie endpoint reports unauthenticated', async () => {
  const store = storage({token:'legacy'});
  const calls=[];
  const user=await readSession(options(async (url,init)=>{calls.push(init);return calls.length===1?response(401):response(200,{id:'legacy-user'});},store));
  assert.equal(user.id,'legacy-user');
  assert.deepEqual(calls[1].headers,{Authorization:'Bearer legacy'});
  assert.equal(store.getItem('token'),'legacy');
});
test('an expired session clears stale local identity and stays signed out', async () => {
  const store = storage({token:'expired',user:'cached'});
  assert.equal(await readSession(options(async()=>response(401),store)),null);
  assert.equal(store.getItem('token'),null);
  assert.equal(store.getItem('user'),null);
});
test('network or service failure cannot erase a valid local session or cause bearer fallback', async () => {
  for (const failure of [async()=>{throw Error('offline');},async()=>response(503),async()=>response(200,{unexpected:true})]) {
    const store=storage({token:'keep',user:'keep-user'});
    await assert.rejects(readSession(options(failure,store)));
    assert.equal(store.getItem('token'),'keep');
    assert.equal(store.getItem('user'),'keep-user');
  }
});
test('both entry points bundle routing and the public guide uses the V2 recorder', () => {
  const index=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const workspace=fs.readFileSync(path.join(__dirname,'workspace.html'),'utf8');
  assert.match(index,/data-copy="hero1"/);
  assert.match(workspace,/id="about-link"/);
  for (const html of [index,workspace]) {
    assert.match(html,/src="\.\/navigation.js"/);
    for(const match of html.matchAll(/(?:src|href)="\.\/([^"?#]+\.(?:js|css|md))"/g)) {
      assert.ok(fs.existsSync(path.join(__dirname,match[1])),match[1]);
    }
  }
  assert.doesNotMatch(index,/query_context|--idempotency-key|edc record|edc get --event/);
  assert.match(index,/edc push/);
  assert.match(index,/record_events/);
  assert.equal(fs.readFileSync(path.join(__dirname,'skills/edc-recorder/SKILL.md'),'utf8'),fs.readFileSync(path.join(__dirname,'../backend/skills/edc-recorder/SKILL.md'),'utf8'));
});

// Execute the shipped landing controller, including its startup session check.
// DOM stubs are constrained to IDs actually present in the public HTML.
async function landingSession(href, fetcher, saved = {}) {
  const vm = require('node:vm');
  const html = fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const ids = new Set([...html.matchAll(/\bid="([^"]+)"/g)].map(match=>match[1]));
  const elements = new Map();
  const element = () => ({textContent:'',value:'',dataset:{},setAttribute(){},addEventListener(){},classList:{toggle(){}}});
  for (const id of ids) elements.set(id,element());
  const ctaLabel=element(), cta={...element(),querySelector:()=>ctaLabel};
  const location=new URL(href), redirects=[];
  location.replace=target=>redirects.push(target);
  const values=new Map(Object.entries(saved));
  const store={getItem:key=>values.get(key)||null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)};
  const context={URL,URLSearchParams,AbortController,setTimeout,clearTimeout,location,localStorage:store,
    navigator:{languages:['en']},history:{replaceState(){}},addEventListener(){},fetch:fetcher,
    document:{cookie:'',documentElement:{lang:''},querySelector:()=>element(),
      querySelectorAll:selector=>selector==='[data-workspace]'?[cta]:[],getElementById:id=>elements.get(id)||null},
    ContextI18n:require('./i18n'),ContextNavigation:require('./navigation'),ContextWorkspaceUtils:require('./workspace-utils')};
  context.window=context;
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'landing-copy.js'),'utf8'),context);
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'landing.js'),'utf8'),context);
  await new Promise(resolve=>setImmediate(resolve));
  return {redirects,ctaLabel,cta,elements,store};
}
test('the actual guest root stays on the landing page',async()=>{
  const page=await landingSession('https://context.integ.life/',async()=>response(401));
  assert.deepEqual(page.redirects,[]);
  assert.equal(page.elements.get('landing-session-status').textContent,'');
  assert.match(page.cta.href,/workspace.html\?locale=en/);
});
test('the actual signed-in root opens the workspace, but About remains accessible',async()=>{
  const fetcher=async()=>response(200,{id:'central'});
  const root=await landingSession('https://context.integ.life/?locale=zh-CN',fetcher);
  assert.deepEqual(root.redirects,['/workspace.html?locale=zh-CN']);
  const about=await landingSession('https://context.integ.life/?page=about&project=p&view=state&locale=zh-CN',fetcher);
  assert.deepEqual(about.redirects,[]);
  assert.equal(about.ctaLabel.textContent,'返回工作区');
  assert.equal(about.cta.href,'/workspace.html?project=p&locale=zh-CN#state');
});
test('the actual central-auth callback is forwarded intact before checking a session',async()=>{
  let requests=0;
  const page=await landingSession('https://context.integ.life/?auth=complete&project=p&locale=ms#plugins',async()=>{requests++;return response(401);});
  assert.equal(requests,0);
  assert.deepEqual(page.redirects,['/workspace.html?project=p&auth=complete&locale=ms#plugins']);
});
test('the actual offline landing remains usable with an honest status',async()=>{
  const page=await landingSession('https://context.integ.life/',async()=>{throw Error('offline');});
  assert.deepEqual(page.redirects,[]);
  assert.match(page.elements.get('landing-session-status').textContent,/could not be checked/);
  assert.match(page.cta.href,/workspace.html/);
});

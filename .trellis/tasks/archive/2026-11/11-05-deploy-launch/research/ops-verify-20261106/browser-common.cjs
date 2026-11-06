const fs=require('node:fs'),path=require('node:path');
const {chromium,expect,request}=require('/home/qlqq/workspace/ROOMusic/frontend/node_modules/@playwright/test');
const tmp='/tmp/roomusic-drill',e='/tmp/roomusic-ops-verify-20261106',base='http://127.0.0.1:18090';
const admin=JSON.parse(fs.readFileSync(tmp+'/admin.json','utf8'));
function priv(name,data){fs.writeFileSync(tmp+'/'+name,JSON.stringify(data),{mode:0o600});fs.chmodSync(tmp+'/'+name,0o600);}
function evidence(name,data){fs.writeFileSync(e+'/'+name,JSON.stringify(data,null,2)+'\n');console.log(JSON.stringify(data));}
function command(text){fs.appendFileSync(e+'/commands.log',new Date().toISOString()+' '+text+'\n');}
async function api(ctx,method,route,data,status=200,extra={}){
 command('HTTP '+method+' '+base+route+' [请求体和凭据不记录]');
 const r=await ctx.fetch(base+route,{method,...(data!==undefined?{data}:{}),headers:{Origin:base,...extra}});
 if(r.status()!==status){let code='';try{code=(await r.json()).error?.code??'';}catch{};throw new Error('API '+method+' '+route.replace(/[a-f0-9-]{36}/g,'{id}')+' HTTP '+r.status()+' expected '+status+' code='+code);}
 return r;
}
async function client(state='http-state.json'){return request.newContext({storageState:fs.existsSync(tmp+'/'+state)?tmp+'/'+state:undefined});}
async function save(ctx,name='http-state.json'){priv(name,await ctx.storageState());}
async function scan(ctx,label){const r=await api(ctx,'POST','/api/v1/scans',undefined,202),created=await r.json(),start=Date.now();let s;
 do{s=await(await api(ctx,'GET','/api/v1/scans/'+created.id)).json();if(s.status!=='running')break;if(Date.now()-start>600000)throw new Error('扫描超时');await new Promise(r=>setTimeout(r,1000));}while(true);
 const summary={status:s.status,seconds:(Date.now()-start)/1000,report_status:s.report_status,diagnostics:s.diagnostics};evidence(label+'-scan.json',summary);priv(label+'-scan-private.json',s);if(s.status!=='succeeded')throw new Error('扫描未成功');return created.id;}
async function launch(){const env=Object.fromEntries(Object.entries(process.env).filter(([k])=>!k.toLowerCase().endsWith('_proxy')));const browser=await chromium.launch({headless:true,env,args:['--no-proxy-server']});const context=await browser.newContext({viewport:{width:1440,height:1000},storageState:tmp+'/http-state.json'});context.setDefaultTimeout(20000);return{browser,context,page:await context.newPage()};}
function failure(err){console.error('测试中止：'+err.name+'；'+(String(err.message).startsWith('API ')?err.message:'详细内容不打印，防止泄露音乐信息或凭据'));process.exitCode=1;}
module.exports={fs,path,chromium,expect,request,tmp,e,base,admin,priv,evidence,api,client,save,scan,launch,failure,command};

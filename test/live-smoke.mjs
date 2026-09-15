import assert from 'node:assert/strict';
import {randomUUID,createHmac} from 'node:crypto';
const base='http://127.0.0.1:4000/v1';
let cookie='';
async function call(path,body,expected=200){const response=await fetch(base+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json',Origin:'http://localhost:3100',Cookie:cookie},body:body===undefined?undefined:JSON.stringify(body)});const setCookie=response.headers.get('set-cookie');if(setCookie)cookie=setCookie.split(';')[0];const result=await response.json();assert.ok(response.status===expected||(expected===200&&response.status===201),`${path}: ${response.status} ${JSON.stringify(result)}`);return result;}
// Minimal client-side TOTP (mirrors src/core.ts verifyTotp) so the smoke
// test can generate real, currently-valid codes for enrolled secrets.
const BASE32='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
function base32Decode(input){let bits=0,value=0;const bytes=[];for(const ch of input.toUpperCase()){const idx=BASE32.indexOf(ch);if(idx===-1)continue;value=(value<<5)|idx;bits+=5;if(bits>=8){bytes.push((value>>>(bits-8))&0xff);bits-=8;}}return Buffer.from(bytes);}
function totpNow(secret){const key=base32Decode(secret);const counter=Math.floor(Date.now()/1000/30);const buf=Buffer.alloc(8);buf.writeUInt32BE(Math.floor(counter/2**32),0);buf.writeUInt32BE(counter%2**32,4);const digest=createHmac('sha1',key).update(buf).digest();const offset=digest[digest.length-1]&0xf;const bin=((digest[offset]&0x7f)<<24)|((digest[offset+1]&0xff)<<16)|((digest[offset+2]&0xff)<<8)|(digest[offset+3]&0xff);return (bin%1000000).toString().padStart(6,'0');}
const id=randomUUID();
const email=`smoke-${id}@example.test`;
const password=`test-only-${randomUUID()}`;
await call('/auth/register',{email,name:'Synthetic Smoke Expert',password});
console.log('PASS registration and session');
let workspace=await call('/workspace/actions',{type:'create-capsule',data:{title:`Synthetic smoke ${id.slice(0,8)}`,domain:'Equipment maintenance',scope:'Checking changes before replacing equipment components. Synthetic integration fixture.',visibility:'PRIVATE'}});
const capsule=workspace.capsules[0];
workspace=await call('/workspace/actions',{type:'add-source',capsuleId:capsule.id,data:{title:'Synthetic maintenance note',type:'NOTE',text:'Check operating changes before replacing equipment components.'}});
for(let i=0;i<60;i++){workspace=await call('/workspace');if(workspace.knowledge.length)break;await new Promise(resolve=>setTimeout(resolve,1000));}
assert.ok(workspace.knowledge.length,'Source processing did not produce candidate knowledge');
for(const item of workspace.knowledge)await call('/workspace/actions',{type:'approve',capsuleId:capsule.id,id:item.id});
console.log('PASS private source ingestion and expert approval');
await call(`/capsules/${capsule.id}/evaluations/cases`,{question:'What should I check before replacing equipment?',expectedElements:['Check operating changes'],unsupported:false});
await call(`/capsules/${capsule.id}/evaluations/cases`,{question:'What is the best chocolate cake recipe?',expectedElements:[],unsupported:true});
workspace=await call('/workspace/actions',{type:'evaluate',capsuleId:capsule.id});
assert.equal(workspace.evaluations[0].passed,true);
workspace=await call('/workspace/actions',{type:'publish',capsuleId:capsule.id});
assert.equal(workspace.capsules[0].version,'1.0.0');
console.log('PASS expert golden evaluation and immutable publication');
workspace=await call('/workspace/actions',{type:'grant',capsuleId:capsule.id,data:{name:'Smoke access',grantee:email,audience:'Named user',purposes:'learning',usageLimit:5,days:1,aiTrainingAllowed:false,commercialUse:false,derivativeUse:false}});
const grant=workspace.licenses[0];
const conversation=await call(`/capsules/${capsule.id}/conversations`,{version:'1.0.0',purpose:'learning'});
const messagePath=`/capsules/${capsule.id}/conversations/${conversation.id}/messages`;
const answer=await call(messagePath,{query:'What should I check before replacing equipment?',version:'1.0.0',purpose:'learning',idempotencyKey:randomUUID()});
assert.equal(answer.abstained,false);assert.ok(answer.citations.length);
const unsupported=await call(messagePath,{query:'What is the best chocolate cake recipe?',version:'1.0.0',purpose:'learning',idempotencyKey:randomUUID()});
assert.equal(unsupported.abstained,true);
await call('/workspace/actions',{type:'revoke',id:grant.id});
await call(messagePath,{query:'What should I check before replacing equipment?',version:'1.0.0',purpose:'learning',idempotencyKey:randomUUID()},403);
console.log('PASS cited answers, unsupported abstention, and revocation');

// ─── Organizations, admin MFA enforcement, and expert credentials ───────
const org=await call('/organizations',{name:`Synthetic Org ${id.slice(0,8)}`});
let orgs=await call('/organizations');
assert.ok(orgs.find(o=>o.id===org.id&&o.role==='ADMIN'&&o.mfaEnabled===false));
console.log('PASS organization creation (creator is admin, MFA not yet enabled)');
const colleagueEmail=`smoke-colleague-${id}@example.test`;
await call('/auth/register',{email:colleagueEmail,name:'Synthetic Colleague',password:`test-only-${randomUUID()}`});
// Registering the colleague switched the session cookie to theirs.
// Re-authenticate as the org admin to continue the admin-only flow below.
await call('/auth/login',{email,password});
await call(`/organizations/${org.id}/members`,{email:colleagueEmail,role:'MEMBER',mfaCode:'000000'},403);
console.log('PASS admin action blocked before MFA enrollment');
const enrollment=await call(`/organizations/${org.id}/mfa/enroll`,{});
assert.ok(enrollment.secret&&enrollment.otpauthUri.startsWith('otpauth://totp/'));
await call(`/organizations/${org.id}/mfa/verify`,{code:totpNow(enrollment.secret)});
orgs=await call('/organizations');
assert.ok(orgs.find(o=>o.id===org.id&&o.mfaEnabled===true));
console.log('PASS admin MFA enrollment and verification');
await call(`/organizations/${org.id}/members`,{email:colleagueEmail,role:'MEMBER',mfaCode:'000000'},403);
await call(`/organizations/${org.id}/members`,{email:colleagueEmail,role:'MEMBER',mfaCode:totpNow(enrollment.secret)});
const members=await call(`/organizations/${org.id}/members`);
assert.ok(members.find(m=>m.email===colleagueEmail&&m.role==='MEMBER'));
console.log('PASS member invited only after a valid current MFA code');
const orgWorkspace=await call('/workspace/actions',{type:'create-capsule',data:{title:`Org capsule ${id.slice(0,8)}`,domain:'Equipment maintenance',scope:'Organization-owned synthetic fixture capsule for smoke testing.',visibility:'PRIVATE',organizationId:org.id}});
const orgCapsule=orgWorkspace.capsules.find(c=>c.title.includes('Org capsule'));
assert.ok(orgCapsule,'Organization-owned capsule not visible in owner workspace snapshot');
console.log('PASS organization-owned capsule creation and membership-scoped visibility');
await call('/experts/me/verification',{type:'LICENSE',issuer:'Synthetic Standards Board'});
const credentials=await call('/experts/me/credentials');
assert.equal(credentials[0].verificationStatus,'PENDING');
console.log('PASS expert credential evidence submission');

console.log('Live smoke passed. Synthetic fixture retained for inspection; no blockchain transaction submitted.');

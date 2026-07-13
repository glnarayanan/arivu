'use strict';
let data;
const token=location.pathname.split('/')[2];
const esc=s=>{const e=document.createElement('span');e.textContent=s;return e.innerHTML};
function render(){let a=data.items.filter(x=>(x.title+' '+x.description+' '+x.domain+' '+x.text).toLowerCase().includes(q.value.toLowerCase()));a.sort((x,y)=>sort.value==='title'?x.title.localeCompare(y.title):sort.value==='old'?x.created_at.localeCompare(y.created_at):y.created_at.localeCompare(x.created_at));list.innerHTML=a.map(x=>'<article><h2><a rel="noopener noreferrer" href="'+esc(x.url)+'">'+esc(x.title)+'</a></h2><p>'+esc(x.description)+'</p><div class=reader>'+x.reader_html+'</div><p>'+x.artifacts.map(z=>'<a href="'+esc(z.url)+'">'+esc(z.type)+'</a>').join(' · ')+'</p></article>').join('')}
q.oninput=render;sort.onchange=render;
fetch('/api/public/shares/'+encodeURIComponent(token)).then(r=>{if(!r.ok)throw new Error();return r.json()}).then(x=>{data=x;t.textContent=data.title;d.textContent=data.description;render()}).catch(()=>{document.body.textContent='This share is unavailable.'});

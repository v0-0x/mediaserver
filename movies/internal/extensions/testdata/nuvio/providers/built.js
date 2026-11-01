/**
 * built - Built from src/built/
 * Generated: 2026-01-08T15:58:02.282Z
 */
"use strict";var a=(t,o,e)=>new Promise((n,s)=>{var f=r=>{try{c(e.next(r))}catch(i){s(i)}},p=r=>{try{c(e.throw(r))}catch(i){s(i)}},c=r=>r.done?n(r.value):Promise.resolve(r.value).then(f,p);c((e=e.apply(t,o)).next())});var E=require("cheerio-without-node-native");function m(t){return a(this,null,function*(){const r=yield fetch("http://nuvio.test/built/"+t);if(!r.ok)throw new Error("built: "+r.status);const j=yield r.json();const $=E.load(j.html);return $("video source").map((i,el)=>({name:"Built",title:$(el).attr("title"),url:$(el).attr("src"),quality:$(el).attr("data-res"),size:Number($(el).attr("data-bytes"))})).get()})}function h(t,o,e,n){return a(this,null,function*(){try{return console.log(`[Built] Request: ${o} ${t}`),yield m(t,o,e,n)}catch(s){return console.error(`[Built] Error: ${s.message}`),[]}})}module.exports={getStreams:h};

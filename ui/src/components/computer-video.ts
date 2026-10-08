// Original DAIMON client for the public RCDP binary video contract. No CUA
// viewer source or generated implementation is bundled.
export interface Frame { sequence:number;codec_epoch:number;geometry_epoch:number;width_px:number;height_px:number;capture_timestamp_us:number;codec:'h264'|'bgra'|'png';keyframe:boolean;session_id:string }
export function parseFrame(buffer:ArrayBuffer):{frame:Frame;data:Uint8Array}{
 if(buffer.byteLength<8||buffer.byteLength>16*1024*1024)throw new Error('invalid media');const v=new DataView(buffer),a=v.getUint32(0),b=v.getUint32(4);if(a>16384||8+a+b!==buffer.byteLength||b===0)throw new Error('invalid media');
 const h=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(new Uint8Array(buffer,8,a))) as {direction:string;message:Frame},f=h.message;
 if(h.direction!=='video'||!f||!['sequence','codec_epoch','geometry_epoch','capture_timestamp_us','width_px','height_px'].every(k=>Number.isSafeInteger(f[k as keyof Frame]))||f.width_px<1||f.height_px<1||f.width_px>1920||f.height_px>1920||f.sequence<0||f.codec_epoch<1||f.geometry_epoch<1||f.capture_timestamp_us<0||typeof f.keyframe!=='boolean'||typeof f.session_id!=='string'||!['h264','bgra','png'].includes(f.codec))throw new Error('invalid media');
 const data=new Uint8Array(buffer,8+a,b);if(f.codec==='bgra'&&data.byteLength!==f.width_px*f.height_px*4)throw new Error('invalid media');return {frame:f,data};
}
export class ComputerVideo {
 private decoder?:VideoDecoder;private epoch=0;private sequence=-1;private waiting=true;private bitmapBusy=false;private closed=false;private generation=0;
 constructor(private canvas:HTMLCanvasElement,private keyframe:()=>void,private live:()=>void,private fail:()=>void){}
 close(){this.closed=true;this.generation++;this.decoder?.close();this.decoder=undefined;this.canvas.getContext('2d')?.clearRect(0,0,this.canvas.width,this.canvas.height);}
 packet(buffer:ArrayBuffer){
  if(this.closed)return;let f:Frame,data:Uint8Array;try{({frame:f,data}=parseFrame(buffer));}catch{this.fail();return}
  if(f.sequence<=this.sequence)return;
  if(f.codec_epoch!==this.epoch||f.codec==='h264'&&this.sequence>=0&&f.sequence!==this.sequence+1){this.waiting=true;this.decoder?.close();this.decoder=undefined;this.generation++;}
  this.epoch=f.codec_epoch;this.sequence=f.sequence;if(this.waiting&&!f.keyframe){this.keyframe();return};this.waiting=false;
  this.canvas.width=f.width_px;this.canvas.height=f.height_px;const ctx=this.canvas.getContext('2d');if(!ctx){this.fail();return}
  if(f.codec==='bgra'){const rgba=new Uint8ClampedArray(data.length);for(let i=0;i<data.length;i+=4){rgba[i]=data[i+2];rgba[i+1]=data[i+1];rgba[i+2]=data[i];rgba[i+3]=255}ctx.putImageData(new ImageData(rgba,f.width_px,f.height_px),0,0);this.live();return}
  if(f.codec==='png'){
   if(this.bitmapBusy)return;this.bitmapBusy=true;const generation=this.generation;
   void createImageBitmap(new Blob([data.slice().buffer],{type:'image/png'})).then(bitmap=>{try{if(!this.closed&&generation===this.generation&&bitmap.width===f.width_px&&bitmap.height===f.height_px){ctx.drawImage(bitmap,0,0);this.live()}}finally{bitmap.close()}}).catch(()=>this.fail()).finally(()=>{this.bitmapBusy=false});return
  }
  try{
   if(!this.decoder){if(typeof VideoDecoder==='undefined')throw new Error('decoder unavailable');let codec='';for(let i=0;i+7<data.length;i++){let start=-1;if(data[i]===0&&data[i+1]===0&&data[i+2]===1)start=i+3;else if(data[i]===0&&data[i+1]===0&&data[i+2]===0&&data[i+3]===1)start=i+4;if(start>=0&&(data[start]&31)===7){codec='avc1.'+[data[start+1],data[start+2],data[start+3]].map(x=>x.toString(16).padStart(2,'0')).join('');break}}if(!codec)throw new Error('missing SPS');
    this.decoder=new VideoDecoder({output:frame=>{try{if(!this.closed){ctx.drawImage(frame,0,0,this.canvas.width,this.canvas.height);this.live()}}finally{frame.close()}},error:()=>{this.waiting=true;this.keyframe()}});this.decoder.configure({codec,optimizeForLatency:true});
   }
   if(this.decoder.decodeQueueSize>=3){this.decoder.reset();this.decoder.close();this.decoder=undefined;this.waiting=true;this.keyframe();return}
   this.decoder.decode(new EncodedVideoChunk({type:f.keyframe?'key':'delta',timestamp:f.capture_timestamp_us,data:data.slice().buffer}));
  }catch{this.fail()}
 }
}

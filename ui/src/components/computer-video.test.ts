import { expect,it,vi,afterEach } from 'vitest';
import { parseFrame,ComputerVideo } from './computer-video';
function packet(width=2,payload=16):ArrayBuffer{const h=new TextEncoder().encode(JSON.stringify({direction:'video',message:{session_id:'media-1',sequence:45,geometry_epoch:1,codec_epoch:1,width_px:width,height_px:2,capture_timestamp_us:100,codec:'bgra',keyframe:true}}));const b=new ArrayBuffer(8+h.length+payload),v=new DataView(b);v.setUint32(0,h.length);v.setUint32(4,payload);new Uint8Array(b,8,h.length).set(h);return b}
it('accepts arbitrary starting sequence and exact bounded binary frames',()=>{expect(parseFrame(packet()).frame.sequence).toBe(45);expect(parseFrame(packet()).data.length).toBe(16)});
it('rejects geometry, body mismatches and oversized headers before decoding',()=>{expect(()=>parseFrame(packet(1921))).toThrow();expect(()=>parseFrame(packet(2,15))).toThrow();const b=packet();new DataView(b).setUint32(0,1<<20);expect(()=>parseFrame(b)).toThrow();expect(()=>parseFrame(new ArrayBuffer(3))).toThrow()});

afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()});
function h264(sequence:number,keyframe=true):ArrayBuffer{
 const data=new Uint8Array([0,0,0,1,0x67,0x42,0,0x1e,0,0]);
 const header=new TextEncoder().encode(JSON.stringify({direction:'video',message:{session_id:'media-1',sequence,geometry_epoch:1,codec_epoch:1,width_px:2,height_px:2,capture_timestamp_us:sequence,codec:'h264',keyframe}}));
 const b=new ArrayBuffer(8+header.length+data.length),v=new DataView(b);v.setUint32(0,header.length);v.setUint32(4,data.length);new Uint8Array(b,8,header.length).set(header);new Uint8Array(b,8+header.length).set(data);return b;
}
it('bounds H264 decode backlog and requires a fresh keyframe before recovery',()=>{
 const reset=vi.fn(),close=vi.fn(),decode=vi.fn(),request=vi.fn(),fail=vi.fn();
 vi.stubGlobal('VideoDecoder',class{decodeQueueSize=3;configure=vi.fn();reset=reset;close=close;decode=decode});
 vi.spyOn(HTMLCanvasElement.prototype,'getContext').mockReturnValue({clearRect:vi.fn(),drawImage:vi.fn()} as unknown as CanvasRenderingContext2D);
 const video=new ComputerVideo(document.createElement('canvas'),request,vi.fn(),fail);
 video.packet(h264(45));expect(reset).toHaveBeenCalledOnce();expect(close).toHaveBeenCalledOnce();expect(decode).not.toHaveBeenCalled();expect(request).toHaveBeenCalledOnce();
 video.packet(h264(46,false));expect(request).toHaveBeenCalledTimes(2);expect(decode).not.toHaveBeenCalled();expect(fail).not.toHaveBeenCalled();video.close();
});
it('does not decode or render packets after the viewer has closed',()=>{
 const live=vi.fn(),fail=vi.fn();vi.spyOn(HTMLCanvasElement.prototype,'getContext').mockReturnValue({clearRect:vi.fn()} as unknown as CanvasRenderingContext2D);
 const video=new ComputerVideo(document.createElement('canvas'),vi.fn(),live,fail);video.close();video.packet(packet());expect(live).not.toHaveBeenCalled();expect(fail).not.toHaveBeenCalled();
});

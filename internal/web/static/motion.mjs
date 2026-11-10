export function validateZones(value) {
  if (!Array.isArray(value) || value.length > 32) throw new Error('invalid_zones');
  return value.map(zone => {
    if (!zone || typeof zone.name !== 'string' || typeof zone.exclude !== 'boolean' || !Array.isArray(zone.points) || zone.points.length < 3 || zone.points.length > 64) throw new Error('invalid_zones');
    const points = zone.points.map(point => {
      if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y) || point.x < 0 || point.x > 1 || point.y < 0 || point.y > 1) throw new Error('invalid_zones');
      return {x: point.x, y: point.y};
    });
    return {name:zone.name, exclude:zone.exclude, points};
  });
}
// Match object-fit:contain so coordinates refer to the video, excluding letterboxing.
export function videoRect(width, height, videoWidth, videoHeight) {
  if (width <= 0 || height <= 0) return {x:0,y:0,width:0,height:0};
  const ratio = videoWidth > 0 && videoHeight > 0 ? videoWidth / videoHeight : width / height;
  const w = Math.min(width, height * ratio), h = w / ratio;
  return {x:(width-w)/2, y:(height-h)/2, width:w, height:h};
}

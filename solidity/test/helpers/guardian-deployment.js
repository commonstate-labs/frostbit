const fs=require('fs'),path=require('path');
                                                           
const G={x:1368015179489954701390400359078579693043519447331113978918064868415326638035n,y:9918110051302171585080402603319702774565515993150576347155970296011118125764n};
function guardianEnvironment(directory){const filename=path.join(directory,'frost-test.json');fs.writeFileSync(filename,JSON.stringify({guardian:{accountPublicKey:G.x.toString(16).padStart(64,'0')+G.y.toString(16).padStart(64,'0')}}));return{VAULT_FROST_CONFIG:filename};}
module.exports={G,guardianEnvironment};

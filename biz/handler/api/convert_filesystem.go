package api

import (
	"FileEngine/biz/dal/db/model"
	filesystem "FileEngine/biz/model/api/filesystem"
	fssvc "FileEngine/biz/service/filesystem"
)

func toFilesystem(f *model.Filesystem) *filesystem.Filesystem {
	return &filesystem.Filesystem{
		ID: int64(f.ID), Name: f.Name, Description: f.Description, Protocol: f.Protocol, BasePath: f.BasePath,
		Host: f.Host, Port: int32(f.Port), Username: f.Username,
		PasswordSet: f.Password != "", PrivateKeySet: f.PrivateKey != "", KeyPassphraseSet: f.KeyPassphrase != "",
		HostKey: f.HostKey, CreatedAt: unix(f.CreatedAt), UpdatedAt: unix(f.UpdatedAt),
	}
}

func fsSaveInput(req *filesystem.SaveFilesystemReq, fsID uint) fssvc.SaveInput {
	return fssvc.SaveInput{
		ID: fsID, Name: req.Name, Description: req.Description,
		Conn: fssvc.Conn{
			Protocol: req.Protocol, BasePath: req.BasePath, Host: req.Host, Port: int(req.Port), Username: req.Username,
			Password: req.Password, PrivateKey: req.PrivateKey, KeyPassphrase: req.KeyPassphrase, HostKey: req.HostKey,
		},
	}
}

func fsProbeInput(req *filesystem.TestFilesystemReq, fsID uint) fssvc.ProbeInput {
	return fssvc.ProbeInput{
		ID: fsID,
		Conn: fssvc.Conn{
			Protocol: req.Protocol, BasePath: req.BasePath, Host: req.Host, Port: int(req.Port), Username: req.Username,
			Password: req.Password, PrivateKey: req.PrivateKey, KeyPassphrase: req.KeyPassphrase, HostKey: req.HostKey,
		},
	}
}

func toProbeResp(r *fssvc.ProbeResult) *filesystem.TestFilesystemResp {
	dirs := make([]*filesystem.ProbeDir, len(r.Dirs))
	for i, d := range r.Dirs {
		dirs[i] = &filesystem.ProbeDir{Name: d.Name, Path: d.Path}
	}
	return &filesystem.TestFilesystemResp{
		Success: r.Success, Error: r.Error, Detail: r.Detail, HostKey: r.HostKey, HostKeyChanged: r.HostKeyChanged, Dirs: dirs,
	}
}

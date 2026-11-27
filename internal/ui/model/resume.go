package model

import tea "charm.land/bubbletea/v2"

// programResume reports how the terminal should restart crush (OSC 7577).
func (m *UI) programResume() *tea.ProgramResume {
	if m.quitting || len(m.resume.args) == 0 {
		return nil
	}
	args := append([]string{}, m.resume.args...)
	if m.session != nil && m.session.ID != "" {
		args = append(args, "--session", m.session.ID)
	}
	dir := m.resume.dir
	if m.com != nil && m.com.Workspace != nil {
		if wd := m.com.Workspace.WorkingDir(); wd != "" {
			dir = wd
		} else if wd := m.com.Workspace.EffectiveWorkingDir(); wd != "" {
			dir = wd
		}
	}
	return &tea.ProgramResume{Args: args, Dir: dir}
}

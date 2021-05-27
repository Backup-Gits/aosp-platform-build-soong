package bp2build

import (
	"android/soong/android"
	"android/soong/bazel"
	"fmt"
	"reflect"
)

// Configurability support for bp2build.

type selects map[string]reflect.Value

func getStringListValues(list bazel.StringListAttribute) (reflect.Value, []selects) {
	value := reflect.ValueOf(list.Value)
	if !list.HasConfigurableValues() {
		return value, []selects{}
	}

	selectValues := make([]selects, 0)
	archSelects := map[string]reflect.Value{}
	for arch, selectKey := range bazel.PlatformArchMap {
		archSelects[selectKey] = reflect.ValueOf(list.GetValueForArch(arch))
	}
	if len(archSelects) > 0 {
		selectValues = append(selectValues, archSelects)
	}

	osSelects := map[string]reflect.Value{}
	osArchSelects := make([]selects, 0)
	for _, os := range android.SortedStringKeys(bazel.PlatformOsMap) {
		selectKey := bazel.PlatformOsMap[os]
		osSelects[selectKey] = reflect.ValueOf(list.GetOsValueForTarget(os))
		archSelects := make(map[string]reflect.Value)
		// TODO(b/187530594): Should we also check arch=CONDITIONS_DEFAULT? (not in AllArches)
		for _, arch := range bazel.AllArches {
			target := os + "_" + arch
			selectKey := bazel.PlatformTargetMap[target]
			archSelects[selectKey] = reflect.ValueOf(list.GetOsArchValueForTarget(os, arch))
		}
		osArchSelects = append(osArchSelects, archSelects)
	}
	if len(osSelects) > 0 {
		selectValues = append(selectValues, osSelects)
	}
	if len(osArchSelects) > 0 {
		selectValues = append(selectValues, osArchSelects...)
	}

	for _, pv := range list.SortedProductVariables() {
		s := make(selects)
		if len(pv.Values) > 0 {
			s[pv.SelectKey()] = reflect.ValueOf(pv.Values)
			s[bazel.ConditionsDefaultSelectKey] = reflect.ValueOf([]string{})
			selectValues = append(selectValues, s)
		}
	}

	return value, selectValues
}

func getLabelValue(label bazel.LabelAttribute) (reflect.Value, []selects) {
	value := reflect.ValueOf(label.Value)
	if !label.HasConfigurableValues() {
		return value, []selects{}
	}

	// Keep track of which arches and oses have been used in case we need to raise a warning
	usedArches := make(map[string]bool)
	usedOses := make(map[string]bool)

	archSelects := map[string]reflect.Value{}
	for arch, selectKey := range bazel.PlatformArchMap {
		archSelects[selectKey] = reflect.ValueOf(label.GetValueForArch(arch))
		if archSelects[selectKey].IsValid() && !isZero(archSelects[selectKey]) {
			usedArches[arch] = true
		}
	}

	osSelects := map[string]reflect.Value{}
	for _, os := range android.SortedStringKeys(bazel.PlatformOsMap) {
		selectKey := bazel.PlatformOsMap[os]
		osSelects[selectKey] = reflect.ValueOf(label.GetOsValueForTarget(os))
		if osSelects[selectKey].IsValid() && !isZero(osSelects[selectKey]) {
			usedOses[os] = true
		}
	}

	osArchSelects := make([]selects, 0)
	for _, os := range android.SortedStringKeys(bazel.PlatformOsMap) {
		archSelects := make(map[string]reflect.Value)
		// TODO(b/187530594): Should we also check arch=CONDITIONS_DEFAULT? (not in AllArches)
		for _, arch := range bazel.AllArches {
			target := os + "_" + arch
			selectKey := bazel.PlatformTargetMap[target]
			archSelects[selectKey] = reflect.ValueOf(label.GetOsArchValueForTarget(os, arch))
			if archSelects[selectKey].IsValid() && !isZero(archSelects[selectKey]) {
				if _, ok := usedArches[arch]; ok {
					fmt.Printf("WARNING: Same arch used twice in LabelAttribute select: arch '%s'\n", arch)
				}
				if _, ok := usedOses[os]; ok {
					fmt.Printf("WARNING: Same os used twice in LabelAttribute select: os '%s'\n", os)
				}
			}
		}
		osArchSelects = append(osArchSelects, archSelects)
	}

	// Because we have to return a single Label, we can only use one select statement
	combinedSelects := map[string]reflect.Value{}
	for k, v := range archSelects {
		combinedSelects[k] = v
	}
	for k, v := range osSelects {
		combinedSelects[k] = v
	}
	for _, osArchSelect := range osArchSelects {
		for k, v := range osArchSelect {
			combinedSelects[k] = v
		}
	}

	return value, []selects{combinedSelects}
}

func getLabelListValues(list bazel.LabelListAttribute) (reflect.Value, []selects) {
	value := reflect.ValueOf(list.Value.Includes)
	if !list.HasConfigurableValues() {
		return value, []selects{}
	}
	var ret []selects

	archSelects := map[string]reflect.Value{}
	for arch, selectKey := range bazel.PlatformArchMap {
		if use, value := labelListSelectValue(selectKey, list.GetValueForArch(arch)); use {
			archSelects[selectKey] = value
		}
	}
	if len(archSelects) > 0 {
		ret = append(ret, archSelects)
	}

	osSelects := map[string]reflect.Value{}
	osArchSelects := []selects{}
	for _, os := range android.SortedStringKeys(bazel.PlatformOsMap) {
		selectKey := bazel.PlatformOsMap[os]
		if use, value := labelListSelectValue(selectKey, list.GetOsValueForTarget(os)); use {
			osSelects[selectKey] = value
		}
		selects := make(map[string]reflect.Value)
		// TODO(b/187530594): Should we also check arch=CONDITIOSN_DEFAULT? (not in AllArches)
		for _, arch := range bazel.AllArches {
			target := os + "_" + arch
			selectKey := bazel.PlatformTargetMap[target]
			if use, value := labelListSelectValue(selectKey, list.GetOsArchValueForTarget(os, arch)); use {
				selects[selectKey] = value
			}
		}
		if len(selects) > 0 {
			osArchSelects = append(osArchSelects, selects)
		}
	}
	if len(osSelects) > 0 {
		ret = append(ret, osSelects)
	}
	ret = append(ret, osArchSelects...)

	return value, ret
}

func labelListSelectValue(selectKey string, list bazel.LabelList) (bool, reflect.Value) {
	if selectKey == bazel.ConditionsDefaultSelectKey || len(list.Includes) > 0 {
		return true, reflect.ValueOf(list.Includes)
	} else if len(list.Excludes) > 0 {
		// if there is still an excludes -- we need to have an empty list for this select & use the
		// value in conditions default Includes
		return true, reflect.ValueOf([]string{})
	}
	return false, reflect.Zero(reflect.TypeOf([]string{}))
}

// prettyPrintAttribute converts an Attribute to its Bazel syntax. May contain
// select statements.
func prettyPrintAttribute(v bazel.Attribute, indent int) (string, error) {
	var value reflect.Value
	var configurableAttrs []selects
	var defaultSelectValue string
	switch list := v.(type) {
	case bazel.StringListAttribute:
		value, configurableAttrs = getStringListValues(list)
		defaultSelectValue = "[]"
	case bazel.LabelListAttribute:
		value, configurableAttrs = getLabelListValues(list)
		defaultSelectValue = "[]"
	case bazel.LabelAttribute:
		value, configurableAttrs = getLabelValue(list)
		defaultSelectValue = "None"
	default:
		return "", fmt.Errorf("Not a supported Bazel attribute type: %s", v)
	}

	var err error
	ret := ""
	if value.Kind() != reflect.Invalid {
		s, err := prettyPrint(value, indent)
		if err != nil {
			return ret, err
		}

		ret += s
	}
	// Convenience function to append selects components to an attribute value.
	appendSelects := func(selectsData selects, defaultValue, s string) (string, error) {
		selectMap, err := prettyPrintSelectMap(selectsData, defaultValue, indent)
		if err != nil {
			return "", err
		}
		if s != "" && selectMap != "" {
			s += " + "
		}
		s += selectMap

		return s, nil
	}

	for _, configurableAttr := range configurableAttrs {
		ret, err = appendSelects(configurableAttr, defaultSelectValue, ret)
		if err != nil {
			return "", err
		}
	}

	return ret, nil
}

// prettyPrintSelectMap converts a map of select keys to reflected Values as a generic way
// to construct a select map for any kind of attribute type.
func prettyPrintSelectMap(selectMap map[string]reflect.Value, defaultValue string, indent int) (string, error) {
	if selectMap == nil {
		return "", nil
	}

	// addConditionsDefault := false

	var selects string
	for _, selectKey := range android.SortedStringKeys(selectMap) {
		if selectKey == bazel.ConditionsDefaultSelectKey {
			// Handle default condition later.
			continue
		}
		value := selectMap[selectKey]
		if isZero(value) {
			// Ignore zero values to not generate empty lists.
			continue
		}
		s, err := prettyPrintSelectEntry(value, selectKey, indent)
		if err != nil {
			return "", err
		}
		// s could still be an empty string, e.g. unset slices of structs with
		// length of 0.
		if s != "" {
			selects += s + ",\n"
		}
	}

	if len(selects) == 0 {
		// No conditions (or all values are empty lists), so no need for a map.
		return "", nil
	}

	// Create the map.
	ret := "select({\n"
	ret += selects

	// Handle the default condition
	s, err := prettyPrintSelectEntry(selectMap[bazel.ConditionsDefaultSelectKey], bazel.ConditionsDefaultSelectKey, indent)
	if err != nil {
		return "", err
	}
	if s == "" {
		// Print an explicit empty list (the default value) even if the value is
		// empty, to avoid errors about not finding a configuration that matches.
		ret += fmt.Sprintf("%s\"%s\": %s,\n", makeIndent(indent+1), bazel.ConditionsDefaultSelectKey, defaultValue)
	} else {
		// Print the custom default value.
		ret += s
		ret += ",\n"
	}

	ret += makeIndent(indent)
	ret += "})"

	return ret, nil
}

// prettyPrintSelectEntry converts a reflect.Value into an entry in a select map
// with a provided key.
func prettyPrintSelectEntry(value reflect.Value, key string, indent int) (string, error) {
	s := makeIndent(indent + 1)
	v, err := prettyPrint(value, indent+1)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", nil
	}
	s += fmt.Sprintf("\"%s\": %s", key, v)
	return s, nil
}

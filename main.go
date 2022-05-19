package main

import "C"
import (
	"encoding/json"
	"fmt"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"golang.org/x/sync/errgroup"
	"log"
	"os"
	"strings"

	"github.com/google/go-containerregistry/cmd/crane/cmd"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/pkg/errors"
)

//export Login
func Login(registryString *C.char, userString *C.char, passwordString *C.char) *C.char {
	defer disableStd()()

	registry := C.GoString(registryString)
	user := C.GoString(userString)
	password := C.GoString(passwordString)

	command := cmd.NewCmdAuthLogin("")
	command.SetArgs([]string{registry, "--username", user, "--password", password})

	if err := command.Execute(); err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to login to registry %s with username %s and given password", registry, user))
	}

	return nil
}

//export Mutate
func Mutate(platformString *C.char, entrypoint *C.char, appendArchive *C.char, baseImage *C.char, tagString *C.char) *C.char {
	defer disableStd()()

	plat := C.GoString(platformString)
	tag := C.GoString(tagString)

	platform, err := v1.ParsePlatform(plat)
	if err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to parse platform %s for target %s", plat, tag))
	}

	var options []crane.Option
	options = append(options, crane.WithPlatform(platform))

	command := cmd.NewCmdMutate(&options)
	command.SetArgs([]string{"--entrypoint", C.GoString(entrypoint), "--append", C.GoString(appendArchive), C.GoString(baseImage), "-t", tag})

	if err := command.Execute(); err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to execute mutate command"))
	}

	return nil
}

//export CreateManifestList
func CreateManifestList(target *C.char, manifestList *C.char) *C.char {
	defer disableStd()()

	manifests := strings.Split(C.GoString(manifestList), ",")

	tag := C.GoString(target)
	targetRef, err := name.NewTag(tag)
	if err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to parse tag %s", tag))
	}

	// create manifest
	targetManifest, err := random.Index(0, 0, 0)
	if err != nil {
		return errorAsCString(errors.Wrapf(err, "can't create image manifest"))
	}
	targetManifest = mutate.IndexMediaType(targetManifest, types.DockerManifestList)

	// retrieve data for each source manifest and collect addendums
	ch := make(chan mutate.IndexAddendum, len(manifests))
	errs := errgroup.Group{}

	opts := crane.GetOptions([]crane.Option{}...)

	for _, m := range manifests {
		manifest := m
		errs.Go(func() error {
			sourceManifestTag, err := name.NewTag(manifest)
			if err != nil {
				return errors.Wrapf(err, "failed to parse manifest tag %s", manifest)
			}

			manifestToAdd, err := remote.Image(sourceManifestTag, opts.Remote...)
			if err != nil {
				return errors.Wrapf(err, "failed to access remote image %s", manifest)
			}

			rcf, err := manifestToAdd.RawConfigFile()
			if err != nil {
				return errors.Wrapf(err, "failed to get config file of %s", manifest)
			}
			var platform v1.Platform
			if err = json.Unmarshal(rcf, &platform); err != nil {
				return errors.Wrapf(err, "failed to unmarshal config file of %s", manifest)
			}

			ch <- mutate.IndexAddendum{
				Add: manifestToAdd,
				Descriptor: v1.Descriptor{
					Platform: &v1.Platform{
						Architecture: platform.Architecture,
						OS:           platform.OS,
						OSVersion:    platform.OSVersion,
						Variant:      platform.Variant,
					},
				},
			}

			return nil
		})
	}

	if err := errs.Wait(); err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to add manifest to manifest list"))
	}

	close(ch)
	var addendums []mutate.IndexAddendum
	for addendum := range ch {
		addendums = append(addendums, addendum)
	}

	targetManifest = mutate.AppendManifests(targetManifest, addendums...)

	if err := remote.WriteIndex(targetRef, targetManifest, opts.Remote...); err != nil {
		return errorAsCString(errors.Wrapf(err, "failed to write manifest list"))
	}

	return nil
}

func errorAsCString(err error) *C.char {
	errorString := fmt.Sprintf("%v", err)
	return C.CString(errorString)
}

func disableStd() func() {
	null, _ := os.Open(os.DevNull)
	sout := os.Stdout
	serr := os.Stderr

	os.Stdout = null
	os.Stderr = null
	log.SetOutput(null)

	return func() {
		defer null.Close()
		os.Stdout = sout
		os.Stderr = serr
		log.SetOutput(os.Stderr)
	}
}

func main() {

}
